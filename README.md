# Dispatch Relay

This application is for relaying SMS messages as a Twilio webhook to a 
preconfigured collection of phone numbers

# Setup

1. Install [Go](https://go.dev/doc/install)
2. Install [MongoDB](https://www.mongodb.com/docs/manual/installation/)

## Environment Variables

- `MONGO_CONNECTION_HOST`: The host for the MongoDB connection.
- `AUTH_TOKEN`: The authentication token for the Twilio webhook.
- `DISPATCH_PHONE_NUMBER`: The phone number to which messages are dispatched.
- `PORT`: The port on which the server will run (default is `4514`
- `TWILIO_AUTH_TOKEN`: The Twilio authentication token for verifying requests.
- `TWILIO_ACCOUNT_SID`: The Twilio account SID for verifying requests.