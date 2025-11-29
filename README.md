# Dispatch Relay

This application is for relaying SMS messages as a Twilio webhook to a 
preconfigured collection of phone numbers

# Setup

1. Install [Go](https://go.dev/doc/install)
2. Install [MongoDB](https://www.mongodb.com/docs/manual/installation/)

## Environment Variables

- `MONGO_CONNECTION_STR`: The connection string for the MongoDB database.
- `AUTH_TOKEN`: The authentication token for the Twilio webhook.
- `PORT`: The port on which the server will run (default is `4514`).
- `TWILIO_AUTH_TOKEN`: The Twilio authentication token for verifying requests.
- `TWILIO_ACCOUNT_SID`: The Twilio account SID for verifying requests.
- `GIN_MODE`: The mode for the Gin framework (default is `release`).

## Testing

```bash
curl -X POST "http://localhost:4514/sms?token=your_auth_token" \
     -H "application/x-www-form-urlencoded" \
     -d 'From=+13735928559'
```