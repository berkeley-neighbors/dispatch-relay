package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/berkeley-neighbors/dispatch-relay/utils"

	"github.com/gin-gonic/gin"
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

		phoneConfig, err := h.getSystemPhoneNumbers(timedCtx)
		if err != nil {
			fmt.Println("Error fetching phone number config:", err)
			ginCtx.String(http.StatusInternalServerError, "Server error")
			return
		}

		staffCollection := h.StaffHandle.Collection()

		var staffMatch Staff
		filter := bson.M{"phone_number": from}

		err = staffCollection.FindOne(timedCtx, filter).Decode(&staffMatch)

		isStaffMember := (err == nil)
		if isStaffMember && !h.Config.SkipStaffIgnore {
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

		if err != nil && err != mongo.ErrNoDocuments {
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
			phoneNumbers, err := h.getActiveStaffPhoneNumbers(timedCtx)
			if err != nil {
				fmt.Println("Error retrieving active staff:", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			fmt.Printf("Messaging %d active staff members\n", len(phoneNumbers))

			// Build staff message using template with variable replacement
			staffMessage := utils.ReplaceTemplateVars(h.Templates.SMSStaffTemplate, map[string]string{
				"from": from,
				"body": body,
				"time": time.Now().Format(time.RFC1123),
			})

			// Send message to all staff members
			h.sendMessageToGroup(phoneConfig.Outbound, phoneNumbers, staffMessage)
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
