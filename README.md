# REST API with Golang, Gin, Kafka, and Aerospike

This repository contains a microservices-based application demonstrating a REST API built with Golang and Gin. It leverages Kafka for messaging and Aerospike as the NoSQL database. Docker Compose is used to orchestrate the application components.

## Features

- **Golang REST API**: Implements HTTP endpoints using the Gin framework.
- **Aerospike Integration**: Persistent data storage with high performance.
- **Kafka for Messaging**:
  - Producer and consumer microservices for event-driven architecture.
  - Configured with a Zookeeper-backed Kafka setup.
- **UI Tools**:
  - Kafka UI for managing topics and messages.
  - Aerospike Data Browser for viewing and managing Aerospike data.
- **Scalable Consumer**: Kafka consumer service with configurable replicas.

## Services

1. **Zookeeper**: Kafka dependency for managing brokers.
2. **Kafka**: Message broker with support for topics and partitions.
3. **Aerospike**: NoSQL database for data persistence.
4. **API**: Golang REST API exposing application endpoints.
5. **Consumer**: Golang-based Kafka consumer with fault tolerance and scalability.
6. **Kafka UI**: Web-based tool for managing Kafka topics and messages.
7. **Aerospike Browser**: UI for browsing Aerospike data.

## Setup

Run the application using Docker Compose:

```bash
docker-compose up
```

## Requirements

- Docker and Docker Compose
- Golang (for local development)
- Kafka UI (default port: `8081`)
- Aerospike Browser (default port: `8082`) 

This repository is a robust starting point for building scalable, event-driven applications with Golang, Kafka, and Aerospike.