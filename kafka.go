package main

import (
	"fmt"

	"github.com/confluentinc/confluent-kafka-go/kafka"
)

func SendLog(key string, value []byte, topic string) {
	producerError := Producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
		Key:            []byte(key),
		Value:          value}, nil)
	if producerError != nil {
		fmt.Println("Error sending message to kafka\n")
	} else {
		fmt.Println("Message sent to kafka\n")
	}
}
