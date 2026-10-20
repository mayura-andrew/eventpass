// SPDX-License-Identifier: Apache-2.0

package domain

// ParticipantStatus is the one status column on a participant row.
type ParticipantStatus string

const (
	StatusPendingApproval ParticipantStatus = "pending_approval"
	StatusWaitlisted      ParticipantStatus = "waitlisted"
	StatusRegistered      ParticipantStatus = "registered"
	StatusConfirmed       ParticipantStatus = "confirmed"
	StatusCheckedIn       ParticipantStatus = "checked_in"
	StatusCancelled       ParticipantStatus = "cancelled"
	StatusRejected        ParticipantStatus = "rejected"
)

// AllStatuses lists statuses in the order dashboards show them.
var AllStatuses = []ParticipantStatus{
	StatusPendingApproval, StatusWaitlisted, StatusRegistered, StatusConfirmed,
	StatusCheckedIn, StatusCancelled, StatusRejected,
}

func (s ParticipantStatus) Valid() bool {
	for _, v := range AllStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// Active statuses hold a valid pass: scanning them can succeed.
func (s ParticipantStatus) Active() bool {
	return s == StatusRegistered || s == StatusConfirmed || s == StatusCheckedIn
}

// progress orders the active statuses. A status-change checkpoint only ever
// moves a participant forward; moving back is an undo or an admin action.
var progress = map[ParticipantStatus]int{
	StatusRegistered: 1,
	StatusConfirmed:  2,
	StatusCheckedIn:  3,
}

// AdminTransitions lists what an admin action may change a status to.
// Checkpoints move statuses through CheckpointMove instead.
var adminTransitions = map[ParticipantStatus][]ParticipantStatus{
	StatusPendingApproval: {StatusRegistered, StatusRejected, StatusCancelled, StatusWaitlisted},
	StatusWaitlisted:      {StatusRegistered, StatusPendingApproval, StatusCancelled, StatusRejected},
	StatusRegistered:      {StatusConfirmed, StatusCheckedIn, StatusCancelled},
	StatusConfirmed:       {StatusRegistered, StatusCheckedIn, StatusCancelled},
	StatusCheckedIn:       {StatusConfirmed, StatusRegistered, StatusCancelled},
	StatusCancelled:       {StatusRegistered, StatusWaitlisted},
	StatusRejected:        {StatusRegistered, StatusPendingApproval},
}

// CanAdminTransition reports whether an admin may move from → to.
func CanAdminTransition(from, to ParticipantStatus) bool {
	for _, s := range adminTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// CheckpointMove decides what a status-change checkpoint does to a
// participant currently in status from. It returns the new status and
// whether anything changes. A participant already at or past the target
// keeps their status (the scan is still logged).
func CheckpointMove(from, target ParticipantStatus) (ParticipantStatus, bool) {
	if !from.Active() || !target.Active() {
		return from, false
	}
	if progress[target] > progress[from] {
		return target, true
	}
	return from, false
}
