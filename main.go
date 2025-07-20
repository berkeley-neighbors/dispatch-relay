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

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	router := gin.Default()

	mongoConnectionStr := os.Getenv("MONGO_CONNECTION_STR")
	requestAuthToken := os.Getenv("AUTH_TOKEN")
	dispatchPhoneNumber := os.Getenv("TWILIO_PHONE_NUMBER")
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

		if err == nil {
			fmt.Println("Open thread found for phone number:", from)
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
			fmt.Println("Error finding threads:", err)
			ginCtx.String(http.StatusInternalServerError, "Server error")
			return
		}

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

		staffMessage := fmt.Sprintf("Dispatch message received\n\nFrom: %s\nMessage: %s\nTime: %s\n\nTeam, please respond", from, body, time.Now().Format(time.RFC1123))
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

		message := &twiml.MessagingMessage{
			Body: "Engaging staff. Please wait for a response.",
		}

		twimlResult, err := twiml.Messages([]twiml.Element{message})

		if err != nil {
			ginCtx.String(http.StatusInternalServerError, err.Error())
		} else {
			ginCtx.Header("Content-Type", "text/xml")
			ginCtx.String(http.StatusOK, twimlResult)
		}
	})

	router.Run(fmt.Sprintf(":%s", port))
}
