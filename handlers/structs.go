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
	StaffHandle    *BoundHandle
	ThreadHandle   *BoundHandle
	ConfigHandle   *BoundHandle
	ScheduleHandle *BoundHandle
	Templates      MessageTemplates
	Config         Config
}

func NewService(client *mongo.Client, databaseName string, config Config, templates MessageTemplates, scheduleDatabaseName string) *handlers {
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
		ScheduleHandle: &BoundHandle{
			Client:  client,
			DbName:  scheduleDatabaseName,
			ColName: "schedules",
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
	PublicID    string        `bson:"id" json:"id"`
	PhoneNumber string        `bson:"phone_number" json:"phone_number"`
	Active      bool          `bson:"active" json:"active"`
}

type Thread struct {
	ID          bson.ObjectID `bson:"_id"`
	PhoneNumber string        `bson:"phone_number"`
	Status      string        `bson:"status"`
	CreatedAt   time.Time     `bson:"created_at"`
}

type Schedule struct {
	ID          bson.ObjectID `bson:"_id,omitempty"`
	UID         int           `bson:"uid"`
	PhoneNumber string        `bson:"phone_number"`
	StartTime   string        `bson:"start_time"`
	EndTime     string        `bson:"end_time"`
	DayOfWeek   int           `bson:"day_of_week"`
	Recurring   bool          `bson:"recurring"`
	Date        string        `bson:"date"`
}

// getOnCallStaffPhoneNumbers returns the phone numbers of staff members
// who are currently on-call based on their schedule entries.
// Falls back to all active staff if no schedules are configured.
func (h *handlers) getOnCallStaffPhoneNumbers(ctx context.Context) ([]string, error) {
	activePhones, err := h.getActiveStaffPhoneNumbers(ctx)
	if err != nil {
		return nil, err
	}

	scheduleCollection := h.ScheduleHandle.Collection()

	count, err := scheduleCollection.CountDocuments(ctx, bson.M{})
	if err != nil {
		log.Printf("Error checking schedule collection, falling back to all active staff: %v", err)
		return activePhones, nil
	}

	if count == 0 {
		log.Println("No schedules configured, using all active staff")
		return activePhones, nil
	}

	now := time.Now()
	currentTime := now.Format("15:04")
	currentDayOfWeek := int(now.Weekday())
	currentDate := now.Format("2006-01-02")

	filter := bson.M{
		"$or": []bson.M{
			{
				"recurring":   true,
				"day_of_week": currentDayOfWeek,
				"start_time":  bson.M{"$lte": currentTime},
				"end_time":    bson.M{"$gte": currentTime},
			},
			{
				"recurring":  false,
				"date":       currentDate,
				"start_time": bson.M{"$lte": currentTime},
				"end_time":   bson.M{"$gte": currentTime},
			},
		},
	}

	cursor, err := scheduleCollection.Find(ctx, filter)
	if err != nil {
		log.Printf("Error querying schedules, falling back to all active staff: %v", err)
		return activePhones, nil
	}
	defer cursor.Close(ctx)

	onCallPhones := make(map[string]bool)
	for cursor.Next(ctx) {
		var schedule Schedule
		if err := cursor.Decode(&schedule); err != nil {
			log.Printf("Error decoding schedule: %v", err)
			continue
		}
		onCallPhones[schedule.PhoneNumber] = true
	}

	if len(onCallPhones) == 0 {
		log.Println("No staff currently on-call, falling back to all active staff")
		return activePhones, nil
	}

	var filteredPhones []string
	for _, phone := range activePhones {
		if onCallPhones[phone] {
			filteredPhones = append(filteredPhones, phone)
		}
	}

	if len(filteredPhones) == 0 {
		log.Println("On-call staff found in schedules but none are active in staff list, falling back to all active staff")
		return activePhones, nil
	}

	log.Printf("Filtered to %d on-call staff members out of %d active", len(filteredPhones), len(activePhones))
	return filteredPhones, nil
}
