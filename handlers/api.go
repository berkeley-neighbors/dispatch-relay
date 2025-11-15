package handlers

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// RenderHTML serves the static HTML page with token authentication
func (h *handlers) RenderHTML() gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		token := ginCtx.Query("token")
		if token != h.Config.RequestAuthToken {
			log.Printf("RenderHTML: Unauthorized access attempt from %s", ginCtx.ClientIP())
			ginCtx.String(http.StatusUnauthorized, "Unauthorized: Invalid or missing token")
			return
		}

		log.Printf("RenderHTML: Serving index.html to %s", ginCtx.ClientIP())
		ginCtx.File("./static/index.html")
	}
}

// ListStaff returns all staff phone numbers
func (h *handlers) ListStaff() gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		token := ginCtx.Query("token")
		if token != h.Config.RequestAuthToken {
			ginCtx.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Invalid or missing token"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), h.Config.Timeout)
		defer cancel()

		staffCollection := h.StaffHandle.Collection()
		cursor, err := staffCollection.Find(ctx, bson.M{})
		if err != nil {
			ginCtx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve staff"})
			return
		}
		defer cursor.Close(ctx)

		var staffList []Staff
		if err := cursor.All(ctx, &staffList); err != nil {
			ginCtx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to decode staff"})
			return
		}

		ginCtx.JSON(http.StatusOK, staffList)
	}
}

// AddStaff adds a new staff phone number
func (h *handlers) AddStaff() gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		token := ginCtx.Query("token")
		if token != h.Config.RequestAuthToken {
			ginCtx.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Invalid or missing token"})
			return
		}

		var request struct {
			PhoneNumber string `json:"phone_number" binding:"required"`
		}

		if err := ginCtx.ShouldBindJSON(&request); err != nil {
			ginCtx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: phone_number is required"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), h.Config.Timeout)
		defer cancel()

		staffCollection := h.StaffHandle.Collection()

		// Check if phone number already exists
		var existingStaff Staff
		err := staffCollection.FindOne(ctx, bson.M{"phone_number": request.PhoneNumber}).Decode(&existingStaff)

		if err == nil {
			ginCtx.JSON(http.StatusConflict, gin.H{"error": "Phone number already exists"})
			return
		} else if err != mongo.ErrNoDocuments {
			ginCtx.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}

		newStaff := Staff{
			ID:          bson.NewObjectID(),
			PhoneNumber: request.PhoneNumber,
			Active:      true,
		}

		_, err = staffCollection.InsertOne(ctx, newStaff)
		if err != nil {
			log.Printf("Error inserting staff: %v", err)
			ginCtx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add staff"})
			return
		}

		ginCtx.JSON(http.StatusCreated, newStaff)

	}
}

// RemoveStaff removes a staff phone number
func (h *handlers) RemoveStaff() gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		token := ginCtx.Query("token")
		if token != h.Config.RequestAuthToken {
			ginCtx.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Invalid or missing token"})
			return
		}

		phoneNumber := ginCtx.Param("phone_number")
		if phoneNumber == "" {
			ginCtx.JSON(http.StatusBadRequest, gin.H{"error": "Phone number is required"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), h.Config.Timeout)
		defer cancel()

		staffCollection := h.StaffHandle.Collection()

		result, err := staffCollection.DeleteOne(ctx, bson.M{"phone_number": phoneNumber})
		if err != nil {
			ginCtx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove staff"})
			return
		}

		if result.DeletedCount == 0 {
			ginCtx.JSON(http.StatusNotFound, gin.H{"error": "Phone number not found"})
			return
		}

		ginCtx.JSON(http.StatusOK, gin.H{
			"message":      "Staff member removed successfully",
			"phone_number": phoneNumber,
		})
	}
}
