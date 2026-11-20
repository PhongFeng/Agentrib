package protocol

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var ticketID = regexp.MustCompile(`^QA-([0-9a-f]{7}|[0-9a-f]{32})-[1-8]$`)

type Ticket struct {
	Bug
	Helpers        []string `json:"helpers,omitempty"`
	Closed         bool     `json:"closed"`
	DeferredReason string   `json:"deferred_reason,omitempty"`
}

func ValidVerified(ids []string, bugs []Bug) bool {
	seen := make(map[string]bool)
	for _, bug := range bugs {
		if bug.ID != "" && seen[bug.ID] {
			return false
		}
		seen[bug.ID] = true
	}
	for _, id := range ids {
		if !ticketID.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// Reconstruct tickets from immutable QA attempts, including older reports without IDs.
// An omitted ticket stays open until QA explicitly verifies it or passes the gate.
func TrackTickets(runs []Run, step string, decisions map[string]QADecision) []Ticket {
	var attempts []Run
	for _, run := range runs {
		if run.StepID == step && run.Result != nil {
			attempts = append(attempts, run)
		}
	}
	slices.SortStableFunc(attempts, func(a, b Run) int { return cmp.Compare(a.Attempt, b.Attempt) })
	var tickets []Ticket
	for _, run := range attempts {
		if run.Status == "succeeded" {
			for i := range tickets {
				if tickets[i].DeferredReason == "" {
					tickets[i].Closed = true
				}
			}
			continue
		}
		if run.Status != "rejected" || !ValidBugs(run.Result.Bugs) {
			continue
		}
		for i := range tickets {
			if slices.Contains(run.Result.Verified, tickets[i].ID) {
				tickets[i].Closed = true
				tickets[i].DeferredReason = ""
			}
		}
		used := make(map[string]bool)
		for i, bug := range run.Result.Bugs {
			index := slices.IndexFunc(tickets, func(t Ticket) bool {
				if used[t.ID] {
					return false
				}
				return t.ID == bug.ID || bug.ID == "" && t.Title == bug.Title && t.Expected == bug.Expected
			})
			if index < 0 {
				bug.ID = fmt.Sprintf("QA-%s-%d", run.ID[:min(7, len(run.ID))], i+1)
				if slices.ContainsFunc(tickets, func(t Ticket) bool { return t.ID == bug.ID }) {
					bug.ID = fmt.Sprintf("QA-%s-%d", run.ID, i+1)
				}
				index = len(tickets)
				tickets = append(tickets, Ticket{Bug: bug})
			} else {
				bug.ID = tickets[index].ID
				reason := tickets[index].DeferredReason
				tickets[index] = Ticket{Bug: bug}
				if bug.Owner == "environment" {
					tickets[index].DeferredReason = reason
				}
			}
			if reason := decisions[run.ID].Deferred[bug.ID]; bug.Owner == "environment" && reason != "" {
				tickets[index].DeferredReason = reason
			}
			if assignment, ok := decisions[run.ID].Assignments[bug.ID]; ok {
				tickets[index].Owner = assignment.Owner
				tickets[index].Helpers = assignment.Helpers
			}
			run.Result.Bugs[i].ID = bug.ID
			used[bug.ID] = true
		}
	}
	return tickets
}

func ValidAssignments(plan map[string]Assignment, bugs []Bug, roles []string) bool {
	if len(plan) != len(bugs) {
		return false
	}
	for _, bug := range bugs {
		a := plan[bug.ID]
		if !slices.Contains(roles, a.Owner) || len(a.Helpers) > len(roles)-1 {
			return false
		}
		for i, helper := range a.Helpers {
			if helper == a.Owner || !slices.Contains(roles, helper) || slices.Contains(a.Helpers[:i], helper) {
				return false
			}
		}
	}
	return true
}

func (r Run) IsProductTriage() bool {
	return r.Spec.RepairOf != "" && strings.HasSuffix(r.StepID, ":triage")
}
