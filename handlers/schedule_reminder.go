package handlers

import (
	"context"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// getSchedulesForDate returns all schedule entries that apply to a given date.
func (h *handlers) getSchedulesForDate(ctx context.Context, date time.Time) ([]Schedule, error) {
	scheduleCollection := h.ScheduleHandle.Collection()

	dayOfWeek := int(date.Weekday())
	dateStr := date.Format("2006-01-02")

	filter := bson.M{
		"$or": []bson.M{
			{
				"always": true,
			},
			{
				"recurring":   true,
				"day_of_week": dayOfWeek,
			},
			{
				"recurring": false,
				"date":      dateStr,
			},
		},
	}

	cursor, err := scheduleCollection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var schedules []Schedule
	for cursor.Next(ctx) {
		var s Schedule
		if err := cursor.Decode(&s); err != nil {
			log.Printf("Error decoding schedule: %v", err)
			continue
		}

		// For recurring schedules, only include if the date is on or after the
		// schedule's start date (recurring events shouldn't apply retroactively).
		if s.Recurring && dateStr < s.Date {
			continue
		}

		schedules = append(schedules, s)
	}

	return schedules, cursor.Err()
}

// phoneNumbersFromSchedules extracts unique phone numbers from a schedule list.
func phoneNumbersFromSchedules(schedules []Schedule) map[string]bool {
	phones := make(map[string]bool)
	for _, s := range schedules {
		phones[s.PhoneNumber] = true
	}
	return phones
}

// filterAlwaysSchedules returns only schedules with the always flag set.
func filterAlwaysSchedules(schedules []Schedule) []Schedule {
	var result []Schedule
	for _, s := range schedules {
		if s.Always {
			result = append(result, s)
		}
	}
	return result
}

// SendScheduleReminders checks for staff who have a schedule block today but
// did not have one yesterday. These staff receive a reminder SMS so they know
// their on-call period is starting. Consecutive on-call days will not trigger
// repeated notifications.
func (h *handlers) SendScheduleReminders(ctx context.Context, reminderTemplate string) {
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)

	todaySchedules, err := h.getSchedulesForDate(ctx, now)
	if err != nil {
		log.Printf("Schedule reminder: error fetching today's schedules: %v", err)
		return
	}

	if len(todaySchedules) == 0 {
		log.Println("Schedule reminder: no schedules today, skipping")
		return
	}

	yesterdaySchedules, err := h.getSchedulesForDate(ctx, yesterday)
	if err != nil {
		log.Printf("Schedule reminder: error fetching yesterday's schedules: %v", err)
		return
	}

	todayPhones := phoneNumbersFromSchedules(todaySchedules)
	yesterdayPhones := phoneNumbersFromSchedules(yesterdaySchedules)

	// Only notify numbers that are on-call today but were NOT on-call yesterday,
	// PLUS any numbers marked as always on-call (they always get reminders).
	alwaysPhones := phoneNumbersFromSchedules(filterAlwaysSchedules(todaySchedules))

	var toNotify []string
	for phone := range todayPhones {
		if alwaysPhones[phone] || !yesterdayPhones[phone] {
			toNotify = append(toNotify, phone)
		}
	}

	if len(toNotify) == 0 {
		log.Println("Schedule reminder: all on-call staff had blocks yesterday, no reminders needed")
		return
	}

	phoneConfig, err := h.getSystemPhoneNumbers(ctx)
	if err != nil {
		log.Printf("Schedule reminder: error fetching phone config: %v", err)
		return
	}

	log.Printf("Schedule reminder: sending reminders to %d staff members", len(toNotify))

	for _, phone := range toNotify {
		h.sendMessageToGroup(phoneConfig.Outbound, []string{phone}, reminderTemplate)
	}
}
