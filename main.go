package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"github.com/twilio/twilio-go/twiml"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	router := gin.Default()

	mongoConnectionHost := os.Getenv("MONGO_CONNECTION_HOST")
	requestAuthToken := os.Getenv("AUTH_TOKEN")
	dispatchPhoneNumber := os.Getenv("DISPATCH_PHONE_NUMBER")
	port := os.Getenv("PORT") // Default to 4514 if not set

	if port == "" {
		port = "4514"
	}

	mongoConnectionPost := 27017

	mongoDatabase := "dispatch_relay"
	staffCollection := "staff"
	threadCollection := "threads"

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

		timedCtx, cancel := context.WithTimeout(context.Background(), timeout)

		defer cancel()

		client, _ := mongo.Connect(options.Client().ApplyURI(fmt.Sprintf("mongodb://%s:%d", mongoConnectionHost, mongoConnectionPost)))

		defer func() {
			if err := client.Disconnect(timedCtx); err != nil {
				panic(err)
			}
		}()

		// Get the message the user sent our Twilio number
		from := ginCtx.PostForm("From")

		staffCollection := client.Database(mongoDatabase).Collection(staffCollection)

		var matchingNumber bson.M

		err := staffCollection.FindOne(timedCtx, bson.D{{Key: "phoneNumber", Value: from}}).Decode(&matchingNumber)

		if err == nil {
			fmt.Println("Number belongs to staff member. Ignoring.")
			ginCtx.String(http.StatusOK, "OK")
			return
		} else if err != mongo.ErrNoDocuments {
			fmt.Println("Error finding staff number:", err)
			ginCtx.String(http.StatusInternalServerError, "Server error")
			return
		}

		threadCollection := client.Database(mongoDatabase).Collection(threadCollection)

		var openThreads bson.M

		err = threadCollection.FindOne(timedCtx, bson.D{{Key: "phoneNumber", Value: from}, {Key: "status", Value: "OPEN"}}).Decode(&openThreads)

		if err != nil {
			fmt.Println("Error finding open thread:", err)
			ginCtx.String(http.StatusInternalServerError, "Server error")
			return
		}

		if err == mongo.ErrNoDocuments {
			fmt.Println("No open thread found for this number.")
			_, err := threadCollection.InsertOne(timedCtx, bson.D{
				{Key: "phoneNumber", Value: from},
				{Key: "status", Value: "OPEN"},
			})
			if err != nil {
				fmt.Println("Error creating thread:", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}
		}

		var allStaffNumbers []bson.M
		cursor, err := staffCollection.Find(timedCtx, bson.D{})

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

		body := ginCtx.PostForm("Body")

		twilioClient := twilio.NewRestClient()

		for _, staffMember := range allStaffNumbers {
			staffPhoneNumber, ok := staffMember["phoneNumber"].(string)

			if !ok {
				fmt.Println("Invalid staff phone number format")
				continue
			}

			params := &twilioApi.CreateMessageParams{}
			params.SetBody(body)
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
			Body: "Engaging dispatch staff. Please wait for a response.",
		}

		twimlResult, err := twiml.Messages([]twiml.Element{message})

		if err != nil {
			ginCtx.String(http.StatusInternalServerError, err.Error())
		} else {
			ginCtx.Header("Content-Type", "text/xml")
			ginCtx.String(http.StatusOK, twimlResult)
		}
	})

	router.Run(fmt.Sprintf(":%s", port)) // Start the server on the specified port
}
