package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aerospike/aerospike-client-go/v7"
	"github.com/segmentio/kafka-go"
)

const (
	kafkaTopic     = "kyc_topic"
	aerospikeHost  = "aerospike"
	aerospikePort  = 3000
	aerospikeNS    = "test"
	aerospikeSet   = "kyc"
	maxMessageSize = 5 * 1024 * 1024 // 5MB
)

var (
	kafkaBroker     = getEnvOrDefault("KAFKA_BROKER", "kafka:29092")
	consumerGroupID = getEnvOrDefault("CONSUMER_GROUP_ID", "kyc-consumer-group")
	consumerID      = fmt.Sprintf("%s-%s", consumerGroupID, generateConsumerID())
)

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func generateConsumerID() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s-%d", hostname, time.Now().UnixNano())
}

type KYCData struct {
	JourneyID string    `json:"journey_id"`
	Image     string    `json:"image"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

func initAerospike() (*aerospike.Client, error) {
	log.Printf("Connecting to Aerospike at %s:%d", aerospikeHost, aerospikePort)
	client, err := aerospike.NewClient(aerospikeHost, aerospikePort)
	if err != nil {
		return nil, err
	}
	log.Printf("Connected to Aerospike successfully")
	return client, nil
}

func initKafkaReader() (*kafka.Reader, error) {
	log.Printf("Initializing Kafka consumer [%s] for topic: %s at %s", consumerID, kafkaTopic, kafkaBroker)

	// Try to establish initial connection to Kafka
	retries := 30
	var lastErr error
	for i := 0; i < retries; i++ {
		conn, err := kafka.Dial("tcp", kafkaBroker)
		if err == nil {
			// Test if we can get the controller
			controller, err := conn.Controller()
			if err == nil {
				log.Printf("Successfully connected to Kafka controller at %s:%d", controller.Host, controller.Port)
				conn.Close()
				break
			}
			conn.Close()
			lastErr = fmt.Errorf("failed to get controller: %v", err)
		} else {
			lastErr = err
		}
		if i == retries-1 {
			return nil, fmt.Errorf("failed to connect to Kafka after %d attempts: %v", retries, lastErr)
		}
		log.Printf("Failed to connect to Kafka (attempt %d/%d): %v", i+1, retries, lastErr)
		time.Sleep(2 * time.Second)
	}

	// Create the reader with consumer group configuration
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:          []string{kafkaBroker},
		Topic:            kafkaTopic,
		GroupID:          consumerGroupID,
		MaxBytes:         maxMessageSize,
		CommitInterval:   time.Second,
		ReadLagInterval:  -1,
		StartOffset:      kafka.FirstOffset,
		RetentionTime:    7 * 24 * time.Hour, // 1 week retention
		MinBytes:         10e3,               // 10KB
		MaxWait:          1 * time.Second,
		ReadBackoffMin:   100 * time.Millisecond,
		ReadBackoffMax:   1 * time.Second,
		RebalanceTimeout: 5 * time.Second,
		QueueCapacity:    10000,
	})

	// Verify topic exists and is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try to fetch metadata for the topic
	conn, err := kafka.DialLeader(ctx, "tcp", kafkaBroker, kafkaTopic, 0)
	if err != nil {
		reader.Close()
		return nil, fmt.Errorf("failed to dial leader: %v", err)
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions()
	if err != nil {
		reader.Close()
		return nil, fmt.Errorf("failed to read partitions: %v", err)
	}

	if len(partitions) == 0 {
		reader.Close()
		return nil, fmt.Errorf("topic %s has no partitions", kafkaTopic)
	}

	log.Printf("Successfully initialized Kafka reader for topic %s with %d partitions", kafkaTopic, len(partitions))
	return reader, nil
}

func main() {
	log.Printf("Starting KYC Consumer Service [%s]...", consumerID)

	// Initialize Aerospike
	asClient, err := initAerospike()
	if err != nil {
		log.Fatalf("Failed to connect to Aerospike: %v", err)
	}
	defer asClient.Close()

	// Initialize Kafka reader with retries
	var reader *kafka.Reader
	retries := 5
	for i := 0; i < retries; i++ {
		reader, err = initKafkaReader()
		if err == nil {
			break
		}
		log.Printf("Failed to initialize Kafka reader (attempt %d/%d): %v", i+1, retries, err)
		if i < retries-1 {
			time.Sleep(5 * time.Second)
		}
	}
	if err != nil {
		log.Fatalf("Failed to initialize Kafka reader after %d attempts: %v", retries, err)
	}
	defer reader.Close()

	log.Printf("KYC Consumer Service [%s] started successfully with max message size: %d bytes",
		consumerID, maxMessageSize)
	var processedCount int64

	// Add graceful shutdown handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		log.Printf("Received shutdown signal, closing consumer [%s]...", consumerID)
		cancel()
	}()

	for {
		// Use context for graceful shutdown
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if err == context.Canceled {
				log.Printf("Context canceled, shutting down consumer [%s]", consumerID)
				break
			}
			log.Printf("Error reading Kafka message: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		processedCount++
		if processedCount%100 == 0 {
			log.Printf("Consumer [%s] processed %d messages so far", consumerID, processedCount)
		}

		var kycData KYCData
		if err := json.Unmarshal(msg.Value, &kycData); err != nil {
			log.Printf("Error unmarshaling message: %v", err)
			continue
		}

		log.Printf("Processing KYC for journey_id: %s, message offset: %d, size: %d bytes",
			kycData.JourneyID, msg.Offset, len(msg.Value))

		// Store in Aerospike
		key, err := aerospike.NewKey(aerospikeNS, aerospikeSet, kycData.JourneyID)
		if err != nil {
			log.Printf("Error creating Aerospike key for journey_id %s: %v",
				kycData.JourneyID, err)
			continue
		}

		bins := aerospike.BinMap{
			"journey_id": kycData.JourneyID,
			"image":      kycData.Image,
			"status":     "PROCESSED",
			"timestamp":  kycData.Timestamp.String(),
		}

		start := time.Now()
		err = asClient.Put(nil, key, bins)
		if err != nil {
			log.Printf("Error storing in Aerospike for journey_id %s: %v",
				kycData.JourneyID, err)
			continue
		}

		log.Printf("Consumer [%s] successfully processed KYC for journey_id: %s in %v",
			consumerID, kycData.JourneyID, time.Since(start))
	}
}
