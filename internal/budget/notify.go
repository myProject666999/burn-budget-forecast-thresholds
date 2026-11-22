package budget

import "time"

// DecideNotification compares the current level with the persisted state and
// decides whether to notify.
//
// Rules:
//   - A different UTC month resets the stored level to normal first.
//   - A rise notifies once and records the new level.
//   - The same level does not repeat.
//   - A fall is recorded silently, so a later rise notifies again.
//   - When data is insufficient and the new level would be lower than the
//     stored one, the stored level is held (not treated as a confident drop);
//     this avoids clearing an alert on low-coverage data. Hysteresis is still
//     an open decision and is not applied here.
func DecideNotification(prev NotificationState, monthKey string, currentLevel int, insufficient bool, now time.Time) NotificationDecision {
	d := NotificationDecision{
		PreviousLevel: prev.Level,
		NewLevel:      prev.Level,
		MonthReset:    false,
		Notify:        false,
		Hold:          false,
		LevelName:     LevelName(currentLevel),
	}

	if prev.Month != monthKey {
		d.MonthReset = true
		d.PreviousLevel = 0
		d.NewLevel = 0
	}

	if insufficient && currentLevel < d.NewLevel {
		d.Hold = true
		d.Reason = "insufficient data; keeping the previous notification level instead of clearing it"
		d.State = NotificationState{Month: monthKey, Level: d.NewLevel, UpdatedAt: now.UTC()}
		return d
	}

	switch {
	case currentLevel > d.NewLevel:
		d.NewLevel = currentLevel
		d.Notify = true
		d.Reason = "level increased"
	case currentLevel < d.NewLevel:
		d.NewLevel = currentLevel
		d.Notify = false
		d.Reason = "level decreased; recorded silently"
	default:
		d.NewLevel = currentLevel
		d.Notify = false
		d.Reason = "level unchanged"
	}

	d.State = NotificationState{Month: monthKey, Level: d.NewLevel, UpdatedAt: now.UTC()}
	return d
}
