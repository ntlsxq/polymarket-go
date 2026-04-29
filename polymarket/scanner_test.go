package polymarket

import (
	"reflect"
	"testing"
	"time"
)

func TestUpcomingDateStringsIncludesTodayThroughWeekAhead(t *testing.T) {
	now := time.Date(2026, time.April, 29, 12, 0, 0, 0, et)

	got := upcomingDateStrings(now, defaultScanDaysAhead)
	want := []string{
		"april-29",
		"april-30",
		"may-1",
		"may-2",
		"may-3",
		"may-4",
		"may-5",
		"may-6",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upcomingDateStrings()=%v want %v", got, want)
	}
}

func TestScanEventSlugsUsesKnownDailyEventPatterns(t *testing.T) {
	got := scanEventSlugs([]string{"bitcoin", "ethereum"}, []string{"april-29"})
	want := []string{
		"bitcoin-above-on-april-29",
		"bitcoin-price-on-april-29",
		"ethereum-above-on-april-29",
		"ethereum-price-on-april-29",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanEventSlugs()=%v want %v", got, want)
	}
}

func TestScanEventSlugsDeduplicatesInputs(t *testing.T) {
	got := scanEventSlugs([]string{"bitcoin", "bitcoin"}, []string{"april-29", "april-29"})
	want := []string{
		"bitcoin-above-on-april-29",
		"bitcoin-price-on-april-29",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanEventSlugs()=%v want %v", got, want)
	}
}
