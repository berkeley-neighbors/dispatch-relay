package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/berkeley-neighbors/dispatch-relay/utils"

	"github.com/gin-gonic/gin"
	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"github.com/twilio/twilio-go/twiml"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func (h *handlers) SMS() gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		value, _ := ginCtx.GetQuery("token")

		if value != h.Config.RequestAuthToken {
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

		timedCtx, cancel := context.WithTimeout(context.Background(), h.Config.Timeout)
		defer cancel()

		staffCollection := h.StaffHandle.Collection()

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

		threadCollection := h.ThreadHandle.Collection()

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

		if !threadExists || h.Config.NotificationStrategy == "ALWAYS" {
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
			staffMessage := utils.ReplaceTemplateVars(h.Templates.SMSStaffTemplate, map[string]string{
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
				params.SetFrom(h.Config.DispatchPhoneNumber)
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
				Body: h.Templates.SMSSenderResponse,
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
	}
}
