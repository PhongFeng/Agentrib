package protocol

import "testing"

func TestPartialReviewKeepsOmittedTicketsOpen(t *testing.T) {
	bug := Bug{Owner: "audio", Title: "Music", Steps: "Play", Expected: "Sound", Actual: "Silent"}
	other := bug
	other.Title = "Click"
	unverified := bug
	unverified.Title = "Alert"
	first := Run{ID: "abcdef10000000000000000000000000", StepID: "qa", Status: "rejected", Result: &Result{Bugs: []Bug{bug, other, unverified}}}
	other.ID, other.Title = "QA-abcdef1-2", "Button feedback"
	next := Run{ID: "abcdef20000000000000000000000000", StepID: "qa", Status: "rejected", Result: &Result{Bugs: []Bug{other}, Verified: []string{"QA-abcdef1-1"}}}
	tickets := TrackTickets([]Run{first, next}, "qa", nil)
	if len(tickets) != 3 || !tickets[0].Closed || tickets[1].ID != other.ID || tickets[1].Title != other.Title || tickets[1].Closed || tickets[2].Closed {
		t.Fatalf("unexpected ticket history: %+v", tickets)
	}
	if ValidVerified([]string{other.ID}, []Bug{other}) {
		t.Fatal("same ticket was both open and verified")
	}
}
