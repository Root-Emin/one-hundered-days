package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

/*
Authorization policy.

Authentication (Day 51/52) answers "who is this?". Authorization answers "may
they do this?" - and the two failure modes are different HTTP statuses:

	401 Unauthorized  no identity, or an identity we could not verify
	403 Forbidden     identity is known and valid, permission is not

Everything about who-may-do-what lives in this file. That is deliberate: the
most common authorization bug is not a wrong rule, it is a check that was
copy-pasted into nine handlers and updated in eight.
*/

var (
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
)

//
// ROLES AND PERMISSIONS
//

type Role string

const (
	RoleGuest  Role = "guest"
	RoleMember Role = "member"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

type Permission string

const (
	PermDocumentRead      Permission = "document:read"
	PermDocumentCreate    Permission = "document:create"
	PermDocumentUpdateOwn Permission = "document:update:own"
	PermDocumentUpdateAny Permission = "document:update:any"
	PermDocumentDelete    Permission = "document:delete"
	PermDocumentPublish   Permission = "document:publish"
	PermUserList          Permission = "user:list"
	PermUserSuspend       Permission = "user:suspend"
	PermAuditRead         Permission = "audit:read"
)

// rolePermissions is the policy table: one place, readable in one screen,
// reviewable in a pull request by someone who does not read Go.
//
// Roles are additive here rather than hierarchical. An explicit table is
// longer than "admin inherits editor", and much harder to get subtly wrong -
// you can see exactly what each role has.
var rolePermissions = map[Role][]Permission{
	RoleGuest: {
		PermDocumentRead,
	},
	RoleMember: {
		PermDocumentRead,
		PermDocumentCreate,
		PermDocumentUpdateOwn,
	},
	RoleEditor: {
		PermDocumentRead,
		PermDocumentCreate,
		PermDocumentUpdateOwn,
		PermDocumentUpdateAny,
		PermDocumentPublish,
	},
	RoleAdmin: {
		PermDocumentRead,
		PermDocumentCreate,
		PermDocumentUpdateOwn,
		PermDocumentUpdateAny,
		PermDocumentDelete,
		PermDocumentPublish,
		PermUserList,
		PermUserSuspend,
		PermAuditRead,
	},
}

func KnownRoles() []Role {
	roles := make([]Role, 0, len(rolePermissions))

	for role := range rolePermissions {
		roles = append(roles, role)
	}

	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })

	return roles
}

func AllPermissions() []Permission {
	seen := make(map[Permission]struct{})

	for _, permissions := range rolePermissions {
		for _, permission := range permissions {
			seen[permission] = struct{}{}
		}
	}

	all := make([]Permission, 0, len(seen))

	for permission := range seen {
		all = append(all, permission)
	}

	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	return all
}

//
// PRINCIPAL
//

// Principal is the authenticated caller. It carries roles, never permissions:
// permissions are derived from the policy table at check time, so a policy
// change takes effect immediately instead of when tokens are re-issued.
type Principal struct {
	UserID int64
	Email  string
	Roles  []Role
}

func (p Principal) HasRole(role Role) bool {
	for _, candidate := range p.Roles {
		if candidate == role {
			return true
		}
	}

	return false
}

// Can reports whether any of the principal's roles grants the permission.
func (p Principal) Can(permission Permission) bool {
	for _, role := range p.Roles {
		for _, granted := range rolePermissions[role] {
			if granted == permission {
				return true
			}
		}
	}

	return false
}

func (p Principal) Permissions() []Permission {
	seen := make(map[Permission]struct{})

	for _, role := range p.Roles {
		for _, permission := range rolePermissions[role] {
			seen[permission] = struct{}{}
		}
	}

	permissions := make([]Permission, 0, len(seen))

	for permission := range seen {
		permissions = append(permissions, permission)
	}

	sort.Slice(permissions, func(i, j int) bool { return permissions[i] < permissions[j] })

	return permissions
}

func (p Principal) String() string {
	roles := make([]string, 0, len(p.Roles))

	for _, role := range p.Roles {
		roles = append(roles, string(role))
	}

	return fmt.Sprintf("user %d (%s) roles=[%s]", p.UserID, p.Email, strings.Join(roles, " "))
}

//
// RESOURCE-AWARE POLICY
//
// Pure RBAC cannot express "you may edit your own document". These functions
// combine the role check with an ownership check, which is where most real
// systems land: roles for coarse capability, resource attributes for the rest.
//

type Document struct {
	ID        int64
	OwnerID   int64
	Title     string
	Body      string
	Published bool
}

type Action string

const (
	ActionRead    Action = "read"
	ActionCreate  Action = "create"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionPublish Action = "publish"
)

// AuthorizeDocument is the single entry point for document decisions. Every
// handler calls this; none of them re-implements the rules.
//
// It returns a wrapped ErrForbidden with a reason for the log, while the HTTP
// layer returns a flat 403 - the caller learns that they may not, not why.
func AuthorizeDocument(principal Principal, action Action, document *Document) error {
	switch action {
	case ActionRead:
		if !principal.Can(PermDocumentRead) {
			return deny(principal, action, "missing document:read")
		}

		// Unpublished drafts are visible to their owner and to anyone who can
		// edit anything.
		if document != nil && !document.Published &&
			document.OwnerID != principal.UserID &&
			!principal.Can(PermDocumentUpdateAny) {
			return deny(principal, action, "draft belongs to another user")
		}

		return nil

	case ActionCreate:
		if !principal.Can(PermDocumentCreate) {
			return deny(principal, action, "missing document:create")
		}

		return nil

	case ActionUpdate:
		if document == nil {
			return deny(principal, action, "no document supplied")
		}

		// Either "edit anything", or "edit my own" plus actual ownership.
		if principal.Can(PermDocumentUpdateAny) {
			return nil
		}

		if principal.Can(PermDocumentUpdateOwn) && document.OwnerID == principal.UserID {
			return nil
		}

		return deny(principal, action, "not the owner and cannot edit others' documents")

	case ActionPublish:
		if !principal.Can(PermDocumentPublish) {
			return deny(principal, action, "missing document:publish")
		}

		return nil

	case ActionDelete:
		// Deliberately strict: deletion is destructive and irreversible here,
		// so ownership is not enough. Only the delete permission opens it.
		if !principal.Can(PermDocumentDelete) {
			return deny(principal, action, "missing document:delete")
		}

		return nil

	default:
		// Unknown action: deny. A policy that defaults to "allow" is a policy
		// that grants every future feature to everyone.
		return deny(principal, action, "unknown action")
	}
}

func deny(principal Principal, action Action, reason string) error {
	return fmt.Errorf("%w: %s may not %s: %s", ErrForbidden, principal, action, reason)
}

// PermissionMatrix renders the policy table for review and documentation.
func PermissionMatrix() string {
	var builder strings.Builder

	roles := KnownRoles()

	builder.WriteString(fmt.Sprintf("%-24s", "PERMISSION"))

	for _, role := range roles {
		builder.WriteString(fmt.Sprintf("%-10s", role))
	}

	builder.WriteString("\n")
	builder.WriteString(strings.Repeat("-", 24+10*len(roles)))
	builder.WriteString("\n")

	for _, permission := range AllPermissions() {
		builder.WriteString(fmt.Sprintf("%-24s", permission))

		for _, role := range roles {
			mark := "-"

			if (Principal{Roles: []Role{role}}).Can(permission) {
				mark = "yes"
			}

			builder.WriteString(fmt.Sprintf("%-10s", mark))
		}

		builder.WriteString("\n")
	}

	return builder.String()
}
