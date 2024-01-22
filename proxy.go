package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"github.com/joho/godotenv"
)

//Generic log struct

type LogRequest struct {
	AccountID string `json:"account_id"`
	UserID    string `json:"user"`
	Path      string `json:"path"`
	Method    string `json:"method"`
	Body      string `json:"body"`
	SentAt    int64  `json:"sent_at"`
}

type LogResponse struct {
	ID         string `json:"id"`
	AccountID  string `json:"account_id"`
	UserID     string `json:"user_id"`
	Path       string `json:"path"`
	Method     string `json:"method"`
	Body       string `json:"body"`
	StatusCode int    `json:"status_code"`
	SentAt     int64  `json:"sent_at"`
}

// Usage log struct

type LogUsage struct {
	ID           string `json:"id"`
	AccountID    string `json:"account_id"`
	UserID       string `json:"user_id"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CreatedTime  int64  `json:"created_time"`
}

type OpenAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage   Usage `json:"usage"`
	Created int64 `json:"created"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

var (
	Producer      *kafka.Producer
	producerError error
	Port          string
)

func init() {
	err := godotenv.Load(".env")

	if err != nil {
		log.Fatal("Error loading .env file")
	}

	Port = getenv("PORT", "8080")

	//connect to kafka
	Producer, producerError = kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers": os.Getenv("CLUSTER_BOOTSRTAP_SERVERS"),
		"security.protocol": "SASL_SSL",
		"sasl.mechanisms":   "PLAIN",
		"sasl.username":     os.Getenv("CLUSTER_API_KEY"),
		"sasl.password":     os.Getenv("CLUSTER_API_SECRET"),
		"acks":              "all"})

	if producerError != nil {
		log.Fatal("Error connecting to kafka")
	} else {
		fmt.Println("Connected to kafka")
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}
	return value
}

func handleRequestAndRedirect(res http.ResponseWriter, req *http.Request) {
	fmt.Println("Received request for", req.URL.Path)
	// Parse the destination server's URL
	url := "https://api.openai.com/v1/" // Replace with the URL of the destination server

	// Read the body
	reqBodyBytes, readErr := io.ReadAll(req.Body)
	if readErr != nil {
		fmt.Println(readErr)
	}
	_ = req.Body.Close() //  must close
	req.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))

	// Create a request to the destination server
	proxyReq, err := http.NewRequest(req.Method, url+req.URL.Path, req.Body)
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}

	var userAPIKey string

	// Copy the headers from the original request to the new request
	for header, values := range req.Header {
		for _, value := range values {
			if header == "X-Api-Key" {
				userAPIKey = value
				fmt.Println("User:", userAPIKey)
			} else if header == "Accept-Encoding" {
				// ignore
			} else {
				proxyReq.Header.Add(header, value)

			}
		}
	}
	// proxyReq.Header.Add("Content-Type", "application/json")

	var body map[string]interface{}
	json.Unmarshal(reqBodyBytes, &body)
	fmt.Println(string(reqBodyBytes))

	userID, ok := body["user"].(string)
	if !ok {
		userID = ""
	}

	// log request
	logRequest := LogRequest{
		AccountID: userAPIKey,
		UserID:    userID,
		Path:      req.URL.Path,
		Method:    req.Method,
		Body:      string(reqBodyBytes),
		SentAt:    time.Now().Unix(),
	}

	logRequestBytes, _ := json.Marshal(logRequest)
	SendLog(userAPIKey, logRequestBytes, "requests")

	// Send the request to the destination server
	client := &http.Client{}

	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}

	// Copy the headers from the response to the original response
	for header, values := range resp.Header {
		for _, value := range values {
			res.Header().Add(header, value)
		}
	}
	res.WriteHeader(resp.StatusCode)

	// Read the res body
	respBodyBytes, readRespErr := io.ReadAll(resp.Body)
	if readRespErr != nil {
		fmt.Println(readRespErr)
	}
	defer resp.Body.Close() //  must close
	resp.Body = io.NopCloser(bytes.NewBuffer(respBodyBytes))
	io.Copy(res, resp.Body)
	fmt.Println("Returning response with status", resp.StatusCode)

	// print body
	fmt.Println(string(respBodyBytes))

	var resBody OpenAIResponse
	json.Unmarshal(respBodyBytes, &resBody)

	// log response
	logResponse := LogResponse{
		ID:         resBody.ID,
		AccountID:  userAPIKey,
		UserID:     userID,
		Path:       req.URL.Path,
		Method:     req.Method,
		Body:       string(respBodyBytes),
		StatusCode: resp.StatusCode,
		SentAt:     time.Now().Unix(),
	}

	logResponseBytes, _ := json.Marshal(logResponse)
	SendLog(userAPIKey, logResponseBytes, "responses")

	// log usage
	if (req.URL.Path == "/chat/completions") && req.Method == "POST" {
		var inputTokens int
		var outputTokens int
		usage := resBody.Usage
		inputTokens = usage.PromptTokens
		outputTokens = usage.CompletionTokens

		logUsage := LogUsage{
			ID:           resBody.ID,
			AccountID:    userAPIKey,
			UserID:       userID,
			Model:        resBody.Model,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			CreatedTime:  resBody.Created,
		}

		logUsageBytes, _ := json.Marshal(logUsage)
		SendLog(userAPIKey, logUsageBytes, "usage")
	}
}

func main() {
	// Start the server
	fmt.Printf("Starting the server on port %s", Port)
	http.HandleFunc("/", handleRequestAndRedirect)
	log.Fatal(http.ListenAndServe(":"+Port, nil))
}
