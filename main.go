package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/berkeley-neighbors/dispatch-relay/handlers"
	"github.com/berkeley-neighbors/dispatch-relay/utils"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func routeByTestParam(prodHandler, testHandler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, exists := c.GetQuery("test")
		if exists {
			log.Printf("Routing to TEST handler")
			testHandler(c)
		} else {
			prodHandler(c)
		}
	}
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	err := godotenv.Load()
	if err != nil {
		fmt.Println("Warning: Error loading .env file, environment variables may not be set")
	}

	router := gin.Default()

	mongoConnectionStr := os.Getenv("MONGO_CONNECTION_STR")
	requestAuthToken := os.Getenv("AUTH_TOKEN")
	notificationMethods := os.Getenv("NOTIFICATION_METHODS")
	notificationStrategy := os.Getenv("NOTIFICATION_STRATEGY")
	scheduleDatabaseName := os.Getenv("SCHEDULE_DATABASE")

	if scheduleDatabaseName == "" {
		scheduleDatabaseName = "dispatch"
	}

	// SMS message templates
	smsStaffTemplate := os.Getenv("SMS_STAFF_MESSAGE_TEMPLATE")
	smsSenderResponse := os.Getenv("SMS_SENDER_RESPONSE_MESSAGE")
	voiceConnectingMessage := os.Getenv("VOICE_CONNECTING_MESSAGE")
	voiceMissedCallStaffMessage := os.Getenv("VOICE_MISSED_CALL_STAFF_MESSAGE")
	voiceMissedCallCallerMessage := os.Getenv("VOICE_MISSED_CALL_CALLER_MESSAGE")

	smsStaffTemplateTest := os.Getenv("SMS_STAFF_MESSAGE_TEMPLATE_TEST")
	smsSenderResponseTest := os.Getenv("SMS_SENDER_RESPONSE_MESSAGE_TEST")
	voiceConnectingMessageTest := os.Getenv("VOICE_CONNECTING_MESSAGE_TEST")
	voiceMissedCallStaffMessageTest := os.Getenv("VOICE_MISSED_CALL_STAFF_MESSAGE_TEST")
	voiceMissedCallCallerMessageTest := os.Getenv("VOICE_MISSED_CALL_CALLER_MESSAGE_TEST")

	// Set defaults if not provided
	if smsStaffTemplate == "" {
		smsStaffTemplate = "Dispatch message received\n\nFrom: {{from}}\nMessage: {{body}}\nTime: {{time}}\n\nTeam, please respond"
	}

	if smsSenderResponse == "" {
		smsSenderResponse = "Engaging staff. Please wait for a response."
	}

	if voiceConnectingMessage == "" {
		voiceConnectingMessage = "Connecting you to dispatch staff. Please hold."
	}

	if voiceMissedCallStaffMessage == "" {
		voiceMissedCallStaffMessage = "MISSED EMERGENCY CALL from {{from}} at {{time}}. Caller could not reach anyone by phone. Please respond immediately."
	}

	if voiceMissedCallCallerMessage == "" {
		voiceMissedCallCallerMessage = "Sorry, no dispatch staff are available to take your call right now. We have sent an urgent message to all staff members. Please try calling back in a few minutes or send a text message for assistance."
	}

	if notificationStrategy == "" {
		notificationStrategy = "THREAD"
	}

	notificationStrategy = utils.UpperString(notificationStrategy)

	port := os.Getenv("PORT")

	if port == "" {
		port = "4514"
	}

	timeout := 60 * time.Second

	if _, found := os.LookupEnv("TWILIO_ACCOUNT_SID"); !found {
		fmt.Println("TWILIO_ACCOUNT_SID is not set")
		return
	}

	if _, found := os.LookupEnv("TWILIO_AUTH_TOKEN"); !found {
		fmt.Println("TWILIO_AUTH_TOKEN is not set")
		return
	}

	enableSMS := false
	enableVoice := false

	if notificationMethods != "" {
		methods := make(map[string]bool)
		for _, method := range utils.ParseEnvironmentVariableList(notificationMethods) {
			methods[method] = true
		}
		enableSMS = methods["SMS"]
		enableVoice = methods["VOICE"]
	}

	log.Printf("SMS enabled: %v, Voice enabled: %v", enableSMS, enableVoice)
	if enableSMS {
		log.Printf("SMS staff message template: %s", smsStaffTemplate)
		log.Printf("SMS sender response: %s", smsSenderResponse)
	}

	// Connect to MongoDB
	client, err := mongo.Connect(options.Client().ApplyURI(mongoConnectionStr))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}

	defer func() {
		if err := client.Disconnect(context.TODO()); err != nil {
			log.Printf("Error disconnecting from MongoDB: %v", err)
		}
	}()

	config := handlers.Config{
		DatabaseName:         "dispatch_relay",
		RequestAuthToken:     requestAuthToken,
		NotificationStrategy: notificationStrategy,
		Timeout:              timeout,
		SkipStaffIgnore:      false,
	}

	testConfig := handlers.Config{
		DatabaseName:         "dispatch_relay_test",
		RequestAuthToken:     requestAuthToken,
		NotificationStrategy: notificationStrategy,
		Timeout:              timeout,
		SkipStaffIgnore:      true,
	}

	templates := handlers.MessageTemplates{
		SMSSenderResponse:            smsSenderResponse,
		SMSStaffTemplate:             smsStaffTemplate,
		VoiceConnectingMessage:       voiceConnectingMessage,
		VoiceMissedCallStaffMessage:  voiceMissedCallStaffMessage,
		VoiceMissedCallCallerMessage: voiceMissedCallCallerMessage,
	}

	testTemplates := handlers.MessageTemplates{
		SMSSenderResponse:            smsSenderResponseTest,
		SMSStaffTemplate:             smsStaffTemplateTest,
		VoiceConnectingMessage:       voiceConnectingMessageTest,
		VoiceMissedCallStaffMessage:  voiceMissedCallStaffMessageTest,
		VoiceMissedCallCallerMessage: voiceMissedCallCallerMessageTest,
	}

	realHandlers := handlers.NewService(client, config.DatabaseName, config, templates, scheduleDatabaseName)
	testHandlers := handlers.NewService(client, testConfig.DatabaseName, testConfig, testTemplates, scheduleDatabaseName)

	// TODO Don't contaminate the environment with prod and test handling
	if enableSMS {
		log.Println("Registering /sms route")
		router.POST("/sms", routeByTestParam(realHandlers.SMS(), testHandlers.SMS()))
	}

	if enableVoice {
		log.Println("Registering /voice and /voice-status routes")
		router.POST("/voice", routeByTestParam(realHandlers.Voice(), testHandlers.Voice()))
		router.POST("/voice-status", routeByTestParam(realHandlers.VoiceStatus(), testHandlers.VoiceStatus()))
	}

	router.GET("/health", func(ginCtx *gin.Context) {
		log.Printf("Health check requested from IP: %s", ginCtx.ClientIP())

		ginCtx.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().Format(time.RFC3339),
			"service":   "dispatch-relay",
		})
	})

	router.HEAD("/health", func(ginCtx *gin.Context) {
		log.Printf("Health check requested from IP: %s", ginCtx.ClientIP())
		ginCtx.Status(http.StatusOK)
	})

	router.Run(fmt.Sprintf(":%s", port))
}
