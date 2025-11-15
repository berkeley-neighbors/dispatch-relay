package handlers

import (
	"time"

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

type handlers struct {
	StaffHandle  *BoundHandle
	ThreadHandle *BoundHandle
	Templates    MessageTemplates
	Config       Config
}

type Config struct {
	RequestAuthToken     string
	DispatchPhoneNumber  string
	NotificationStrategy string
	Timeout              time.Duration
}

func NewService(client *mongo.Client, config Config, templates MessageTemplates) *handlers {
	return &handlers{
		StaffHandle: &BoundHandle{
			Client:  client,
			DbName:  "dispatch_relay",
			ColName: "staff",
		},
		ThreadHandle: &BoundHandle{
			Client:  client,
			DbName:  "dispatch_relay",
			ColName: "threads",
		},
		Templates: templates,
		Config:    config,
	}
}

type Staff struct {
	ID          bson.ObjectID `bson:"_id"`
	PhoneNumber string        `bson:"phone_number"`
	Active      bool          `bson:"active"`
}

type Thread struct {
	ID          bson.ObjectID `bson:"_id"`
	PhoneNumber string        `bson:"phone_number"`
	Status      string        `bson:"status"`
	CreatedAt   time.Time     `bson:"created_at"`
}
