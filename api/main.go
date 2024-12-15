package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/aerospike/aerospike-client-go/v7"
	"github.com/gin-gonic/gin"
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
	kafkaBroker = getEnvOrDefault("KAFKA_BROKER", "kafka:29092")
)

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

type KYCRequest struct {
	JourneyID string `json:"journey_id" binding:"required"`
	Image     string `json:"image" binding:"required"`
}

type KYCData struct {
	JourneyID string    `json:"journey_id"`
	Image     string    `json:"image"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

var (
	kafkaWriter *kafka.Writer
	asClient    *aerospike.Client
)

func initKafka() error {
	log.Printf("Initializing Kafka connection to %s", kafkaBroker)

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
			return fmt.Errorf("failed to connect to Kafka after %d attempts: %v", retries, lastErr)
		}
		log.Printf("Failed to connect to Kafka (attempt %d/%d): %v", i+1, retries, lastErr)
		time.Sleep(2 * time.Second)
	}

	// Create Kafka writer with retries
	kafkaWriter = &kafka.Writer{
		Addr:                   kafka.TCP(kafkaBroker),
		Topic:                  kafkaTopic,
		Balancer:               &kafka.LeastBytes{},
		MaxAttempts:            10,
		BatchSize:              1,
		BatchTimeout:           10 * time.Millisecond,
		WriteTimeout:           30 * time.Second,
		RequiredAcks:           kafka.RequireOne,
		AllowAutoTopicCreation: true,
	}

	// Verify topic exists and is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try to fetch metadata for the topic
	conn, err := kafka.DialLeader(ctx, "tcp", kafkaBroker, kafkaTopic, 0)
	if err != nil {
		return fmt.Errorf("failed to dial leader: %v", err)
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions()
	if err != nil {
		return fmt.Errorf("failed to read partitions: %v", err)
	}

	if len(partitions) == 0 {
		// Try to create the topic if it doesn't exist
		if err := createTopic(kafkaBroker, kafkaTopic); err != nil {
			return fmt.Errorf("failed to create topic: %v", err)
		}
		log.Printf("Created topic %s successfully", kafkaTopic)
	} else {
		log.Printf("Topic %s exists with %d partitions", kafkaTopic, len(partitions))
	}

	log.Printf("Kafka connection initialized successfully with max message size: %d bytes", maxMessageSize)
	return nil
}

func createTopic(broker, topic string) error {
	conn, err := kafka.Dial("tcp", broker)
	if err != nil {
		return err
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		return err
	}

	controllerConn, err := kafka.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		return err
	}
	defer controllerConn.Close()

	topicConfigs := []kafka.TopicConfig{
		{
			Topic:             topic,
			NumPartitions:     1,
			ReplicationFactor: 1,
		},
	}

	err = controllerConn.CreateTopics(topicConfigs...)
	if err != nil {
		return err
	}

	return nil
}

func initAerospike() error {
	log.Printf("Connecting to Aerospike at %s:%d", aerospikeHost, aerospikePort)
	client, err := aerospike.NewClient(aerospikeHost, aerospikePort)
	if err != nil {
		return err
	}
	asClient = client
	log.Printf("Connected to Aerospike successfully")
	return nil
}

func submitKYC(c *gin.Context) {
	var request KYCRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("Error binding JSON request: %v", err)
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	imageSize := len(request.Image)
	log.Printf("Received KYC request for journey_id: %s, image size: %d bytes",
		request.JourneyID, imageSize)

	// Check image size
	if imageSize > maxMessageSize {
		log.Printf("Image size too large: %d bytes (max: %d bytes)", imageSize, maxMessageSize)
		c.JSON(400, gin.H{
			"error": fmt.Sprintf("Image size too large. Maximum allowed size is %d bytes", maxMessageSize),
		})
		return
	}

	kycData := KYCData{
		JourneyID: request.JourneyID,
		Image:     request.Image,
		Status:    "PENDING",
		Timestamp: time.Now(),
	}

	value, err := json.Marshal(kycData)
	if err != nil {
		log.Printf("Error marshaling KYC data: %v", err)
		c.JSON(500, gin.H{"error": "Failed to marshal data"})
		return
	}

	log.Printf("Publishing message to Kafka for journey_id: %s", request.JourneyID)
	err = kafkaWriter.WriteMessages(context.Background(),
		kafka.Message{
			Value: value,
		},
	)

	if err != nil {
		log.Printf("Error writing to Kafka: %v", err)
		c.JSON(500, gin.H{"error": "Failed to write to Kafka"})
		return
	}

	log.Printf("Successfully published KYC request to Kafka for journey_id: %s", request.JourneyID)
	c.JSON(200, gin.H{"message": "KYC submitted successfully", "journey_id": request.JourneyID})
}

func getKYCStatus(c *gin.Context) {
	journeyID := c.Param("journeyID")
	if journeyID == "" {
		log.Printf("Error: Journey ID is missing in request")
		c.JSON(400, gin.H{"error": "Journey ID is required"})
		return
	}

	log.Printf("Checking KYC status for journey_id: %s", journeyID)

	key, err := aerospike.NewKey(aerospikeNS, aerospikeSet, journeyID)
	if err != nil {
		log.Printf("Error creating Aerospike key: %v", err)
		c.JSON(500, gin.H{"error": "Failed to create key"})
		return
	}

	record, err := asClient.Get(nil, key)
	if err != nil {
		if err == aerospike.ErrKeyNotFound {
			log.Printf("KYC record not found for journey_id: %s", journeyID)
			c.JSON(404, gin.H{"error": "KYC record not found"})
			return
		}
		log.Printf("Error fetching from Aerospike: %v", err)
		c.JSON(500, gin.H{"error": "Failed to fetch from Aerospike"})
		return
	}

	log.Printf("Successfully retrieved KYC status for journey_id: %s, status: %v",
		journeyID, record.Bins["status"])
	c.JSON(200, record.Bins)
}

func main() {
	log.Printf("Starting KYC API Service...")

	// Initialize Kafka with retries
	retries := 5
	var err error
	for i := 0; i < retries; i++ {
		err = initKafka()
		if err == nil {
			break
		}
		log.Printf("Failed to initialize Kafka (attempt %d/%d): %v", i+1, retries, err)
		if i < retries-1 {
			time.Sleep(5 * time.Second)
		}
	}
	if err != nil {
		log.Fatalf("Failed to initialize Kafka after %d attempts: %v", retries, err)
	}
	defer kafkaWriter.Close()

	// Initialize Aerospike
	if err := initAerospike(); err != nil {
		log.Fatalf("Failed to connect to Aerospike: %v", err)
	}
	defer asClient.Close()

	// Initialize Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s %s %d %s \"%s\" %s\"\n",
			param.ClientIP,
			param.TimeStamp.Format(time.RFC1123),
			param.Method,
			param.Path,
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	}))

	api := r.Group("/api")
	{
		api.POST("/kyc", submitKYC)
		api.GET("/kyc/:journeyID", getKYCStatus)
	}

	log.Printf("KYC API Service started on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
