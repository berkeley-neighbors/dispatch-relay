package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type BoundHandle struct {
	Client  *mongo.Client
	DbName  string
	ColName string
}

func (b *BoundHandle) Collection() *mongo.Collection {
	return b.Client.Database(b.DbName).Collection(b.ColName)
}

type MessageTemplates struct {
	VoiceConnectingMessage       string
	VoiceMissedCallStaffMessage  string
	VoiceMissedCallCallerMessage string
	SMSSenderResponse            string
	SMSStaffTemplate             string
}

type Config struct {
	DatabaseName         string
	RequestAuthToken     string
	NotificationStrategy string
	SkipStaffIgnore      bool
	Timeout              time.Duration
}

type PhoneNumberConfig struct {
	Inbound  string `bson:"inbound" json:"inbound"`
	Outbound string `bson:"outbound" json:"outbound"`
}

type handlers struct {
	StaffHandle  *BoundHandle
	ThreadHandle *BoundHandle
	ConfigHandle *BoundHandle
	Templates    MessageTemplates
	Config       Config
}

func NewService(client *mongo.Client, databaseName string, config Config, templates MessageTemplates) *handlers {
	return &handlers{
		StaffHandle: &BoundHandle{
			Client:  client,
			DbName:  databaseName,
			ColName: "staff",
		},
		ThreadHandle: &BoundHandle{
			Client:  client,
			DbName:  databaseName,
			ColName: "threads",
		},
		ConfigHandle: &BoundHandle{
			Client:  client,
			DbName:  databaseName,
			ColName: "config",
		},
		Templates: templates,
		Config:    config,
	}
}

func (h *handlers) getSystemPhoneNumbers(ctx context.Context) (*PhoneNumberConfig, error) {
	configCollection := h.ConfigHandle.Collection()

	// Fetch both numbers together
	cursor, err := configCollection.Find(ctx, bson.M{
		"key": bson.M{"$in": []string{"inbound_number", "outbound_number"}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch phone number configs: %w", err)
	}
	defer cursor.Close(ctx)

	var inbound, outbound string

	for cursor.Next(ctx) {
		var config struct {
			Key   string `bson:"key"`
			Value string `bson:"value"`
		}

		if err := cursor.Decode(&config); err != nil {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}

		switch config.Key {
		case "inbound_number":
			inbound = config.Value
		case "outbound_number":
			outbound = config.Value
		}
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	return &PhoneNumberConfig{
		Inbound:  inbound,
		Outbound: outbound,
	}, nil
}

func (h *handlers) sendMessageToGroup(fromNumber string, phoneNumbers []string, message string) {
	twilioClient := twilio.NewRestClient()

	for _, phoneNumber := range phoneNumbers {
		params := &twilioApi.CreateMessageParams{}
		params.SetBody(message)
		params.SetFrom(fromNumber)
		params.SetTo(phoneNumber)

		resp, err := twilioClient.Api.CreateMessage(params)
		if err != nil {
			log.Printf("Error sending message to %s: %v", phoneNumber, err)
		} else {
			log.Printf("Sent message to %s", phoneNumber)
			if resp.Body != nil {
				log.Printf("Response: %s", *resp.Body)
			}
		}
	}
}

func (h *handlers) getActiveStaffPhoneNumbers(ctx context.Context) ([]string, error) {
	staffCollection := h.StaffHandle.Collection()

	cursor, err := staffCollection.Find(ctx, bson.M{"active": true})
	if err != nil {
		return nil, fmt.Errorf("error retrieving active staff: %w", err)
	}
	defer cursor.Close(ctx)

	var phoneNumbers []string
	for cursor.Next(ctx) {
		var staff Staff
		if err := cursor.Decode(&staff); err != nil {
			log.Printf("Error decoding staff member: %v", err)
			continue
		}
		phoneNumbers = append(phoneNumbers, staff.PhoneNumber)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	return phoneNumbers, nil
}

type Staff struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"_id"`
	PhoneNumber string        `bson:"phone_number" json:"phone_number"`
	Active      bool          `bson:"active" json:"active"`
}

type Thread struct {
	ID          bson.ObjectID `bson:"_id"`
	PhoneNumber string        `bson:"phone_number"`
	Status      string        `bson:"status"`
	CreatedAt   time.Time     `bson:"created_at"`
}
