package handlers

import (
	"context"
	"fmt"
	"log"
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

type VoiceHandlerOptions struct {
	DispatchPhoneNumber          string
	VoiceMissedCallStaffMessage  string
	VoiceMissedCallCallerMessage string
	VoiceConnectingMessage       string
}

type VoiceStatusHandlerOptions struct {
	DispatchPhoneNumber          string
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

		staffCollection := h.StaffHandle.Collection()

		var staffMatch Staff
		filter := bson.M{"phone_number": from}

		err := staffCollection.FindOne(timedCtx, filter).Decode(&staffMatch)

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
			twimlXml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
    <Say>` + h.Templates.VoiceConnectingMessage + `</Say>
    <Dial timeout="20" callerId="` + h.Config.DispatchPhoneNumber + `" action="/voice-status?token=` + h.Config.RequestAuthToken + `&amp;from=` + from + `">`

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

			staffCollection := h.StaffHandle.Collection()

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
			staffMessage := utils.ReplaceTemplateVars(h.Templates.VoiceMissedCallStaffMessage, map[string]string{
				"from": from,
				"time": time.Now().Format(time.RFC1123),
			})

			for _, staffMember := range allStaffNumbers {
				staffPhoneNumber, ok := staffMember["phone_number"].(string)
				if !ok {
					continue
				}

				params := &twilioApi.CreateMessageParams{}
				params.SetBody(staffMessage)
				params.SetFrom(h.Config.DispatchPhoneNumber)
				params.SetTo(staffPhoneNumber)

				_, err := twilioClient.Api.CreateMessage(params)
				if err != nil {
					log.Printf("Error sending SMS to %s: %v", staffPhoneNumber, err)
				} else {
					log.Printf("Sent missed call SMS to %s", staffPhoneNumber)
				}
			}

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
