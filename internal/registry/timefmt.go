package registry

import "time"

const timestampLayout = time.RFC3339Nano

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(timestampLayout)
}

func formatSecondTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

func parseTimestamp(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func parseSQLiteTimestamp(value string) (time.Time, error) {
	return time.ParseInLocation(time.DateTime, value, time.UTC)
}

func formatSQLiteTimestamp(value time.Time) string {
	return value.UTC().Format(time.DateTime)
}

func formatLocalTime(value time.Time, layout string) string {
	return value.Local().Format(layout)
}
