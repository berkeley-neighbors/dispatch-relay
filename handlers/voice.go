package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/berkeley-neighbors/dispatch-relay/utils"

	"github.com/gin-gonic/gin"
	"github.com/twilio/twilio-go/twiml"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type VoiceHandlerOptions struct {
	VoiceMissedCallStaffMessage  string
	VoiceMissedCallCallerMessage string
	VoiceConnectingMessage       string
}

type VoiceStatusHandlerOptions struct {
	VoiceMissedCallStaffMessage  string
	VoiceMissedCallCallerMessage string
}

func (h *handlers) Voice() gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		value, _ := ginCtx.GetQuery("token")

		if value != h.Config.RequestAuthToken {
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

		timedCtx, cancel := context.WithTimeout(context.Background(), h.Config.Timeout)
		defer cancel()

		// Fetch phone number configuration from MongoDB
		phoneConfig, err := h.getSystemPhoneNumbers(timedCtx)
		if err != nil {
			fmt.Println("Error fetching phone number config:", err)
			ginCtx.String(http.StatusInternalServerError, "Server error")
			return
		}

		staffCollection := h.StaffHandle.Collection()
		blockListCollection := h.BlockListHandle.Collection()

		var staffMatch Staff
		var blockMatch BlockedNumber
		filter := bson.M{"phone_number": from}

		// Is Staff?
		err = staffCollection.FindOne(timedCtx, filter).Decode(&staffMatch)
		isStaffMember := (err == nil)

		if isStaffMember && !h.Config.SkipStaffIgnore {
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

		// Is Blocked?
		err = blockListCollection.FindOne(timedCtx, filter).Decode(&blockMatch)
		isBlocked := (err == nil)

		if isBlocked {
			fmt.Println("Number is blocked:", from)
			ginCtx.String(http.StatusOK, "")
			return
		}

		if err != nil && err != mongo.ErrNoDocuments {
			fmt.Printf("Error finding staff number %s: %v", from, err)
			ginCtx.String(http.StatusInternalServerError, "Server error")
			return
		}

		threadCollection := h.ThreadHandle.Collection()

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

		phoneNumbers, err := h.getActiveStaffPhoneNumbers(timedCtx)
		if err != nil {
			fmt.Printf("Error retrieving active staff: %v", err)
			ginCtx.String(http.StatusInternalServerError, "Server error")
			return
		}

		// If caller is a staff member in test mode, remove their number from the list
		if isStaffMember && h.Config.SkipStaffIgnore {
			filteredNumbers := make([]string, 0, len(phoneNumbers))
			for _, phoneNumber := range phoneNumbers {
				if phoneNumber != from {
					filteredNumbers = append(filteredNumbers, phoneNumber)
				} else {
					fmt.Printf("Skipping caller's own number from dial list: %s\n", from)
				}
			}
			phoneNumbers = filteredNumbers
		}

		if len(phoneNumbers) == 0 {
			fmt.Printf("No active staff members found in database")
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

		fmt.Printf("Attempting to connect caller %s to %d active staff members", from, len(phoneNumbers))

		// Create TwiML to forward the call to staff members using raw XML
		var twimlResult string
		if len(phoneNumbers) > 0 {
			twimlXml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
    <Say>` + h.Templates.VoiceConnectingMessage + `</Say>
    <Dial timeout="20" callerId="` + phoneConfig.Inbound + `" action="/voice-status?token=` + h.Config.RequestAuthToken + `&amp;from=` + from + `">`

			for _, phoneNumber := range phoneNumbers {
				twimlXml += `<Number>` + phoneNumber + `</Number>`
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
	}
}

func (h *handlers) VoiceStatus() gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		value, _ := ginCtx.GetQuery("token")

		if value != h.Config.RequestAuthToken {
			ginCtx.String(http.StatusUnauthorized, "Unauthorized")
			return
		}

		from, _ := ginCtx.GetQuery("from")
		dialCallStatus := ginCtx.PostForm("DialCallStatus")
		callSid := ginCtx.PostForm("CallSid")

		log.Printf("Call status update - From: %s, Status: %s, CallSid: %s", from, dialCallStatus, callSid)

		if dialCallStatus == "completed" {
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
			timedCtx, cancel := context.WithTimeout(context.Background(), h.Config.Timeout)
			defer cancel()

			// Fetch phone number configuration from MongoDB
			phoneConfig, err := h.getSystemPhoneNumbers(timedCtx)
			if err != nil {
				log.Printf("Error fetching phone number config: %v", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			phoneNumbers, err := h.getActiveStaffPhoneNumbers(timedCtx)
			if err != nil {
				log.Printf("Error retrieving active staff: %v", err)
				ginCtx.String(http.StatusInternalServerError, "Server error")
				return
			}

			// Send SMS notifications since call wasn't answered
			staffMessage := utils.ReplaceTemplateVars(h.Templates.VoiceMissedCallStaffMessage, map[string]string{
				"from": from,
				"time": time.Now().Format(time.RFC1123),
			})

			// Send message to all active staff members
			h.sendMessageToGroup(phoneConfig.Outbound, phoneNumbers, staffMessage)

			say := &twiml.VoiceSay{
				Message: h.Templates.VoiceMissedCallCallerMessage,
			}

			twimlResult, err := twiml.Voice([]twiml.Element{say})
			if err != nil {
				ginCtx.String(http.StatusInternalServerError, err.Error())
			} else {
				ginCtx.Header("Content-Type", "text/xml")
				ginCtx.String(http.StatusOK, twimlResult)
			}
		}
	}
}
