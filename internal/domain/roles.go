// SPDX-License-Identifier: Apache-2.0

// Package domain holds every business rule: roles and permissions, statuses,
// field validation, checkpoint rules. It does no I/O, so it is tested without
// Google or a network.
package domain

// WorkspaceRole applies to every event.
type WorkspaceRole string

const (
	WorkspaceOwner WorkspaceRole = "owner"
	WorkspaceAdmin WorkspaceRole = "admin"
	WorkspaceNone  WorkspaceRole = "none"
)

func (r WorkspaceRole) Valid() bool {
	return r == WorkspaceOwner || r == WorkspaceAdmin || r == WorkspaceNone
}

// EventRole is granted per event in the EventRoles tab.
type EventRole string

const (
	EventAdmin  EventRole = "admin"
	EventStaff  EventRole = "staff"
	EventViewer EventRole = "viewer"
	EventNone   EventRole = ""
)

func (r EventRole) Valid() bool {
	return r == EventAdmin || r == EventStaff || r == EventViewer
}

// Perm is what a handler declares it needs.
type Perm string

const (
	// Public routes and routes for any signed-in principal.
	PermPublic        Perm = "public"
	PermAuthenticated Perm = "authenticated"
	// PermParticipant is a participant session acting on its own records.
	PermParticipant Perm = "participant"

	// Workspace scope.
	PermWorkspaceRead  Perm = "workspace.read"
	PermWorkspaceWrite Perm = "workspace.write"
	PermUsersManage    Perm = "users.manage"
	PermAuditRead      Perm = "audit.read"
	PermSystemHealth   Perm = "system.health"
	PermEventCreate    Perm = "event.create"
	PermEventListAll   Perm = "event.list_all"
	PermEventListOwn   Perm = "event.list_own"
	PermUsersRead      Perm = "users.read"

	// Event scope.
	PermEventRead         Perm = "event.read"
	PermEventWrite        Perm = "event.write"
	PermEventRolesManage  Perm = "event.roles_manage"
	PermParticipantList   Perm = "participant.list"
	PermParticipantView   Perm = "participant.view"
	PermParticipantLookup Perm = "participant.lookup"
	PermParticipantWrite  Perm = "participant.write"
	PermFieldsRead        Perm = "fields.read"
	PermFieldsWrite       Perm = "fields.write"
	PermCheckpointsRead   Perm = "checkpoints.read"
	PermCheckpointsWrite  Perm = "checkpoints.write"
	PermCheckpointRecord  Perm = "checkpoint.record"
	PermStatsRead         Perm = "stats.read"
)

// EventScoped reports whether a permission is checked against a role in one
// event (the route then has an {eventID} path value).
func (p Perm) EventScoped() bool {
	_, ok := eventPerms[EventViewer][p]
	if ok {
		return true
	}
	_, ok = eventPerms[EventAdmin][p]
	return ok
}

var workspacePerms = map[WorkspaceRole]map[Perm]struct{}{
	WorkspaceOwner: set(PermWorkspaceRead, PermWorkspaceWrite, PermUsersManage, PermAuditRead,
		PermSystemHealth, PermEventCreate, PermEventListAll, PermEventListOwn, PermUsersRead),
	WorkspaceAdmin: set(PermWorkspaceRead, PermEventCreate, PermEventListAll, PermEventListOwn, PermUsersRead),
	WorkspaceNone:  set(PermEventListOwn),
}

var eventPerms = map[EventRole]map[Perm]struct{}{
	EventAdmin: set(PermEventRead, PermEventWrite, PermEventRolesManage, PermParticipantList,
		PermParticipantView, PermParticipantLookup, PermParticipantWrite, PermFieldsRead,
		PermFieldsWrite, PermCheckpointsRead, PermCheckpointsWrite, PermCheckpointRecord, PermStatsRead),
	EventStaff: set(PermEventRead, PermParticipantView, PermParticipantLookup, PermFieldsRead,
		PermCheckpointsRead, PermCheckpointRecord),
	EventViewer: set(PermEventRead, PermParticipantList, PermParticipantView, PermFieldsRead,
		PermCheckpointsRead, PermStatsRead),
}

func set(ps ...Perm) map[Perm]struct{} {
	m := make(map[Perm]struct{}, len(ps))
	for _, p := range ps {
		m[p] = struct{}{}
	}
	return m
}

// EffectiveEventRole combines the workspace role with a grant for one event.
// Owners and workspace admins administer every event.
func EffectiveEventRole(ws WorkspaceRole, granted EventRole) EventRole {
	if ws == WorkspaceOwner || ws == WorkspaceAdmin {
		return EventAdmin
	}
	return granted
}

// UserCan reports whether a user (not a participant) holds a permission.
// eventRole is the grant for the event in question, EventNone when the
// permission is workspace scoped or there is no grant.
func UserCan(ws WorkspaceRole, eventRole EventRole, p Perm) bool {
	switch p {
	case PermPublic, PermAuthenticated:
		return true
	case PermParticipant:
		return false
	}
	if _, ok := workspacePerms[ws][p]; ok {
		return true
	}
	role := EffectiveEventRole(ws, eventRole)
	if role == EventNone {
		return false
	}
	_, ok := eventPerms[role][p]
	return ok
}

// ParticipantCan reports whether a participant session holds a permission.
// Participants only ever act on their own records through /me routes.
func ParticipantCan(p Perm) bool {
	return p == PermPublic || p == PermAuthenticated || p == PermParticipant
}

// ViewerLevel is how much of a participant record someone may see.
type ViewerLevel int

const (
	LevelStaff ViewerLevel = iota + 1 // staff and viewers
	LevelAdmin
)

// LevelFor returns the field visibility level for an effective event role.
func LevelFor(role EventRole) ViewerLevel {
	if role == EventAdmin {
		return LevelAdmin
	}
	return LevelStaff
}
