package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"github.com/twilio/twilio-go/twiml"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Staff struct {
	ID          bson.ObjectID `bson:"_id"`
	PhoneNumber string        `bson:"phone_number"`
}

type Thread struct {
	ID          bson.ObjectID `bson:"_id"`
	PhoneNumber string        `bson:"phone_number"`
	Status      string        `bson:"status"`
	CreatedAt   time.Time     `bson:"created_at"`
}

func parseEnvironmentVariableList(methods string) []string {
	if methods == "" {
		return []string{}
	}

	var result []string
	for _, method := range splitAndTrim(methods, ",") {
		normalized := upperString(method)
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}

func splitAndTrim(s string, sep string) []string {
	var result []string
	for _, part := range splitString(s, sep) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitString(s string, sep string) []string {
	if s == "" {
		return []string{}
	}

	var result []string
	current := ""

	for i := 0; i < len(s); i++ {
		if string(s[i]) == sep {
			result = append(result, current)
			current = ""
		} else {
			current += string(s[i])
		}
	}
	result = append(result, current)
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)

	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}

	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}

	return s[start:end]
}

func upperString(s string) string {
	upper := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			upper += string(c - 32)
		} else {
			upper += string(c)
		}
	}

	return upper
}

// replaceTemplateVars replaces template variables in format {{varname}} with actual values
func replaceTemplateVars(template string, vars map[string]string) string {
	result := template

	for key, value := range vars {
		placeholder := "{{" + key + "}}"
		result = replaceString(result, placeholder, value)
	}

	return result
}

func replaceString(s string, old string, new string) string {
	if s == "" || old == "" {
		return s
	}

	result := ""
	i := 0

	for i < len(s) {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}

	return result
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
	dispatchPhoneNumber := os.Getenv("TWILIO_PHONE_NUMBER")
	callerIdPhoneNumber := os.Getenv("DISPATCH_PHONE_NUMBER")
	notificationMethods := os.Getenv("NOTIFICATION_METHODS")
	notificationStrategy := os.Getenv("NOTIFICATION_STRATEGY")

	// SMS message templates
	smsStaffTemplate := os.Getenv("SMS_STAFF_MESSAGE_TEMPLATE")
	smsSenderResponse := os.Getenv("SMS_SENDER_RESPONSE_MESSAGE")

	// Set defaults if not provided
	if smsStaffTemplate == "" {
		smsStaffTemplate = "Dispatch message received\n\nFrom: {{from}}\nMessage: {{body}}\nTime: {{time}}\n\nTeam, please respond"
	}

	if smsSenderResponse == "" {
		smsSenderResponse = "Engaging staff. Please wait for a response."
	}

	if notificationStrategy == "" {
		notificationStrategy = "THREAD"
	}

	notificationStrategy = upperString(notificationStrategy)

	port := os.Getenv("PORT")

	if port == "" {
		port = "4514"
	}

	mongoDatabase := "dispatch_relay"
	staffCollectionName := "staff"
	threadCollectionName := "threads"

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
		for _, method := range parseEnvironmentVariableList(notificationMethods) {
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

	if enableSMS {
		log.Println("Registering /sms route")
		router.POST("/sms", func(ginCtx *gin.Context) {
			value, _ := ginCtx.GetQuery("token")

			if value != requestAuthToken {
				ginCtx.String(http.StatusUnauthorized, "Unauthorized")
				return
			}

			from := ginCtx.PostForm("From")
			body := ginCtx.PostForm("Body")

			if from == "" {
				fmt.Println("From number is empty")
				ginCtx.String(http.StatusBadRequest, "From number is required")
				return
			}

			if body == "" {
				fmt.Println("Body is empty")
				ginCtx.String(http.StatusBadRequest, "Your text appears to be empty. Please resend")
				return
			}

			timedCtx, cancel := context.WithTimeout(context.Background(), timeout)

			defer cancel()

			client, _ := mongo.Connect(options.Client().ApplyURI(mongoConnectionStr))

			defer func() {
				if err := client.Disconnect(timedCtx); err != nil {
					panic(err)
				}
			}()

			staffCollection := client.Database(mongoDatabase).Collection(staffCollectionName)

			var staffMatch Staff

			filter := bson.M{"phone_number": from}

			err := staffCollection.FindOne(timedCtx, filter).Decode(&staffMatch)

			if err == nil {
				fmt.Println("Number belongs to staff member. Ignoring.")
				doc, _ := twiml.CreateDocument()
				xml, err := twiml.ToXML(doc)

				if err != nil {
					fmt.Println("Error creating TwiML document:", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}

				ginCtx.Header("Content-Type", "text/xml")
				ginCtx.String(http.StatusOK, xml)

				return
			}

			if err != mongo.ErrNoDocuments {
				fmt.Println("Error finding staff number:", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			threadCollection := client.Database(mongoDatabase).Collection(threadCollectionName)

			var openThread Thread
			filter = bson.M{"phone_number": from, "status": "OPEN"}

			err = threadCollection.FindOne(timedCtx, filter).Decode(&openThread)

			threadExists := (err == nil)

			if err != nil && err != mongo.ErrNoDocuments {
				fmt.Println("Error finding threads:", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			if !threadExists {
				fmt.Println("Starting new thread for phone number:", from)

				_, err = threadCollection.InsertOne(timedCtx, bson.M{
					"phone_number": from,
					"status":       "OPEN",
					"created_at":   time.Now(),
				})

				if err != nil {
					fmt.Println("Error creating thread:", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}
			}

			if !threadExists || notificationStrategy == "ALWAYS" {
				var allStaffNumbers []bson.M
				cursor, err := staffCollection.Find(timedCtx, bson.M{})

				if err != nil {
					fmt.Println("Error retrieving staff numbers:", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}

				defer cursor.Close(timedCtx)

				for cursor.Next(timedCtx) {
					var staffMember bson.M
					if err := cursor.Decode(&staffMember); err != nil {
						fmt.Println("Error decoding staff member:", err)
						ginCtx.String(http.StatusInternalServerError, "Server error")
						return
					}
					allStaffNumbers = append(allStaffNumbers, staffMember)
				}

				if err := cursor.Err(); err != nil {
					fmt.Println("Cursor error:", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}

				twilioClient := twilio.NewRestClient()

				fmt.Printf("Messaging %d staff members\n", len(allStaffNumbers))

				// Build staff message using template with variable replacement
				staffMessage := replaceTemplateVars(smsStaffTemplate, map[string]string{
					"from": from,
					"body": body,
					"time": time.Now().Format(time.RFC1123),
				})

				for _, staffMember := range allStaffNumbers {
					staffPhoneNumber, ok := staffMember["phone_number"].(string)

					fmt.Printf("Contacting: %s\n", staffPhoneNumber)
					if !ok {
						fmt.Println("Invalid staff phone number format")
						continue
					}

					params := &twilioApi.CreateMessageParams{}
					params.SetBody(staffMessage)
					params.SetFrom(dispatchPhoneNumber)
					params.SetTo(staffPhoneNumber)

					resp, err := twilioClient.Api.CreateMessage(params)
					if err != nil {
						fmt.Println("Error sending message:", err.Error())
					} else {
						if resp.Body != nil {
							fmt.Println(*resp.Body)
						} else {
							fmt.Println(resp.Body)
						}
					}
				}
			} else {
				fmt.Println("Skipping staff notification")
			}

			var senderResponse string

			if threadExists {
				fmt.Println("Open thread found for phone number:", from)
				doc, _ := twiml.CreateDocument()
				xml, err := twiml.ToXML(doc)
				if err != nil {
					fmt.Println("Error creating TwiML document:", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}

				senderResponse = xml
			} else {
				message := &twiml.MessagingMessage{
					Body: smsSenderResponse,
				}

				xml, err := twiml.Messages([]twiml.Element{message})

				if err != nil {
					ginCtx.String(http.StatusInternalServerError, err.Error())
					return
				}

				senderResponse = xml
			}

			ginCtx.Header("Content-Type", "text/xml")
			ginCtx.String(http.StatusOK, senderResponse)
		})
	}

	if enableVoice {
		log.Println("Registering /voice and /voice-status routes")
		router.POST("/voice", func(ginCtx *gin.Context) {
			value, _ := ginCtx.GetQuery("token")

			if value != requestAuthToken {
				ginCtx.String(http.StatusUnauthorized, "Unauthorized")
				return
			}

			from := ginCtx.PostForm("From")
			callSid := ginCtx.PostForm("CallSid")

			if from == "" {
				fmt.Println("From number is empty")
				ginCtx.String(http.StatusBadRequest, "From number is required")
				return
			}

			log.Printf("Received voice call from: %s (CallSid: %s)", from, callSid)

			timedCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			client, err := mongo.Connect(options.Client().ApplyURI(mongoConnectionStr))
			if err != nil {
				log.Printf("Failed to connect to MongoDB: %v", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			defer func() {
				if err := client.Disconnect(timedCtx); err != nil {
					log.Printf("Error disconnecting from MongoDB: %v", err)
				}
			}()

			staffCollection := client.Database(mongoDatabase).Collection(staffCollectionName)

			var staffMatch Staff
			filter := bson.M{"phone_number": from}

			err = staffCollection.FindOne(timedCtx, filter).Decode(&staffMatch)

			if err == nil {
				fmt.Println("Call from staff member. Ignoring.")
				// Return empty TwiML to hang up
				doc, _ := twiml.CreateDocument()
				xml, err := twiml.ToXML(doc)

				if err != nil {
					fmt.Println("Error creating TwiML document:", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}

				ginCtx.Header("Content-Type", "text/xml")
				ginCtx.String(http.StatusOK, xml)
				return
			}

			if err != mongo.ErrNoDocuments {
				fmt.Printf("Error finding staff number %s: %v", from, err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			threadCollection := client.Database(mongoDatabase).Collection(threadCollectionName)

			var openThread Thread
			filter = bson.M{"phone_number": from, "status": "OPEN"}

			err = threadCollection.FindOne(timedCtx, filter).Decode(&openThread)

			if err != mongo.ErrNoDocuments && err != nil {
				fmt.Printf("Error finding threads for %s: %v", from, err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			// Create thread if it doesn't exist
			if err == mongo.ErrNoDocuments {
				fmt.Printf("Creating new thread for voice call from %s", from)
				_, err = threadCollection.InsertOne(timedCtx, bson.M{
					"phone_number": from,
					"status":       "OPEN",
					"created_at":   time.Now(),
				})

				if err != nil {
					fmt.Printf("Error creating thread for %s: %v", from, err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}
			}

			var allStaffNumbers []bson.M
			cursor, err := staffCollection.Find(timedCtx, bson.M{})

			if err != nil {
				fmt.Printf("Error retrieving staff numbers: %v", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			defer cursor.Close(timedCtx)

			for cursor.Next(timedCtx) {
				var staffMember bson.M
				if err := cursor.Decode(&staffMember); err != nil {
					fmt.Printf("Error decoding staff member: %v", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}
				allStaffNumbers = append(allStaffNumbers, staffMember)
			}

			if err := cursor.Err(); err != nil {
				fmt.Printf("Cursor error: %v", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			if len(allStaffNumbers) == 0 {
				fmt.Printf("No staff members found in database")
				say := &twiml.VoiceSay{
					Message: "Sorry, no dispatch staff are currently available. Please try again later or send a text message.",
				}

				twimlResult, err := twiml.Voice([]twiml.Element{say})
				if err != nil {
					ginCtx.String(http.StatusInternalServerError, err.Error())
				} else {
					ginCtx.Header("Content-Type", "text/xml")
					ginCtx.String(http.StatusOK, twimlResult)
				}
				return
			}

			fmt.Printf("Attempting to connect caller %s to %d staff members", from, len(allStaffNumbers))

			// Create TwiML to forward the call to staff members using raw XML
			var twimlResult string
			if len(allStaffNumbers) > 0 {
				// Build XML string for multiple numbers to try in sequence
				twimlXml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
    <Say>Connecting you to dispatch staff. Please hold.</Say>
    <Dial timeout="20" callerId="` + callerIdPhoneNumber + `" action="/voice-status?token=` + requestAuthToken + `&amp;from=` + from + `">`

				// Add each staff number to try
				for _, staffMember := range allStaffNumbers {
					staffPhoneNumber, ok := staffMember["phone_number"].(string)
					if ok {
						twimlXml += `<Number>` + staffPhoneNumber + `</Number>`
					}
				}

				twimlXml += `
    </Dial>
</Response>`

				twimlResult = twimlXml
				err = nil
			} else {
				// Fallback if no valid numbers
				fallbackSay := &twiml.VoiceSay{
					Message: "Sorry, no dispatch staff are currently available. Please try again later.",
				}
				twimlResult, err = twiml.Voice([]twiml.Element{fallbackSay})
			}

			if err != nil {
				fmt.Printf("Error creating TwiML: %v", err)
				ginCtx.String(http.StatusInternalServerError, err.Error())
			} else {
				ginCtx.Header("Content-Type", "text/xml")
				ginCtx.String(http.StatusOK, twimlResult)
			}
		})

		router.POST("/voice-status", func(ginCtx *gin.Context) {
			value, _ := ginCtx.GetQuery("token")

			if value != requestAuthToken {
				ginCtx.String(http.StatusUnauthorized, "Unauthorized")
				return
			}

			from, _ := ginCtx.GetQuery("from")
			dialCallStatus := ginCtx.PostForm("DialCallStatus")
			callSid := ginCtx.PostForm("CallSid")

			log.Printf("Call status update - From: %s, Status: %s, CallSid: %s", from, dialCallStatus, callSid)

			if dialCallStatus == "completed" {
				// Call was successfully connected and completed
				say := &twiml.VoiceSay{
					Message: "Thank you for contacting dispatch.",
				}

				twimlResult, err := twiml.Voice([]twiml.Element{say})
				if err != nil {
					ginCtx.String(http.StatusInternalServerError, err.Error())
				} else {
					ginCtx.Header("Content-Type", "text/xml")
					ginCtx.String(http.StatusOK, twimlResult)
				}
			} else {
				// Call was not answered by any staff member
				timedCtx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()

				client, err := mongo.Connect(options.Client().ApplyURI(mongoConnectionStr))
				if err != nil {
					log.Printf("Failed to connect to MongoDB: %v", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}

				defer func() {
					if err := client.Disconnect(timedCtx); err != nil {
						log.Printf("Error disconnecting from MongoDB: %v", err)
					}
				}()

				staffCollection := client.Database(mongoDatabase).Collection(staffCollectionName)

				var allStaffNumbers []bson.M
				cursor, err := staffCollection.Find(timedCtx, bson.M{})
				if err != nil {
					log.Printf("Error retrieving staff numbers: %v", err)
					ginCtx.String(http.StatusInternalServerError, "Server error")
					return
				}
				defer cursor.Close(timedCtx)

				for cursor.Next(timedCtx) {
					var staffMember bson.M
					if err := cursor.Decode(&staffMember); err != nil {
						continue
					}
					allStaffNumbers = append(allStaffNumbers, staffMember)
				}

				// Send SMS notifications since call wasn't answered
				twilioClient := twilio.NewRestClient()
				staffMessage := fmt.Sprintf("MISSED EMERGENCY CALL from %s at %s. Caller could not reach anyone by phone. Please respond immediately.", from, time.Now().Format(time.RFC1123))

				for _, staffMember := range allStaffNumbers {
					staffPhoneNumber, ok := staffMember["phone_number"].(string)
					if !ok {
						continue
					}

					params := &twilioApi.CreateMessageParams{}
					params.SetBody(staffMessage)
					params.SetFrom(dispatchPhoneNumber)
					params.SetTo(staffPhoneNumber)

					_, err := twilioClient.Api.CreateMessage(params)
					if err != nil {
						log.Printf("Error sending SMS to %s: %v", staffPhoneNumber, err)
					} else {
						log.Printf("Sent missed call SMS to %s", staffPhoneNumber)
					}
				}

				say := &twiml.VoiceSay{
					Message: "Sorry, no dispatch staff are available to take your call right now. We have sent an urgent message to all staff members. Please try calling back in a few minutes or send a text message for assistance.",
				}

				twimlResult, err := twiml.Voice([]twiml.Element{say})
				if err != nil {
					ginCtx.String(http.StatusInternalServerError, err.Error())
				} else {
					ginCtx.Header("Content-Type", "text/xml")
					ginCtx.String(http.StatusOK, twimlResult)
				}
			}
		})
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
