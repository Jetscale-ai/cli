package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// User contains only reconciliation metadata, never credentials or invitations.
type User struct {
	ID                string     `json:"id"`
	Email             string     `json:"email"`
	IsSuperuser       *bool      `json:"is_superuser"`
	Status            string     `json:"status"`
	Company           *string    `json:"company"`
	SignupCompletedAt *time.Time `json:"signup_completed_at"`
}

// Project normalizes company to a UUID and keeps nullable fields present.
func (u User) Project() Record {
	var company, superuser, completed any
	if u.Company != nil {
		company = *u.Company
	}
	if u.IsSuperuser != nil {
		superuser = *u.IsSuperuser
	}
	if u.SignupCompletedAt != nil {
		completed = u.SignupCompletedAt.Format(time.RFC3339Nano)
	}
	return Record{"id": u.ID, "email": u.Email, "is_superuser": superuser, "status": u.Status, "company": company, "signup_completed_at": completed}
}

// ValidateUserEmail accepts one exact address, not a name, prefix or display name.
func ValidateUserEmail(email string) error {
	address, err := mail.ParseAddress(email)
	if err != nil || len(email) > 254 || address.Address != email || strings.ContainsAny(email, "\r\n\t ") {
		return errors.New("email must be one exact email address without whitespace or a display name")
	}
	return nil
}

func userFromRecord(record Record) (User, error) {
	u := User{ID: record.ID(), Email: record.String("email"), Status: record.String("status")}
	id, valid := resourceUUID(u.ID)
	if !valid || ValidateUserEmail(u.Email) != nil {
		return User{}, ErrInvalidResponse
	}
	u.ID = id.String()
	switch u.Status {
	case "ACTIVE", "PENDING_ACTIVATION", "DEACTIVATED", "CANCELLED", "TERMINATED":
	default:
		return User{}, ErrInvalidResponse
	}
	if value, exists := record["is_superuser"]; exists && value != nil {
		v, ok := value.(bool)
		if !ok {
			return User{}, ErrInvalidResponse
		}
		u.IsSuperuser = &v
	}
	if record["company"] != nil {
		id, valid := resourceUUID(record.String("company"))
		if !valid {
			return User{}, ErrInvalidResponse
		}
		v := id.String()
		u.Company = &v
	}
	if value := record["signup_completed_at"]; value != nil {
		text, ok := value.(string)
		if !ok {
			return User{}, ErrInvalidResponse
		}
		completed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || completed.IsZero() {
			return User{}, ErrInvalidResponse
		}
		u.SignupCompletedAt = &completed
	}
	return u, nil
}

func (c *Client) usersActor(ctx context.Context) error {
	actor, err := c.Actor(ctx)
	if err != nil {
		return err
	}
	superuser, known := actor.Bool("is_superuser")
	if _, valid := resourceUUID(actor.ID()); !valid || !known || !superuser || actor.String("status") != "ACTIVE" {
		return &APIError{Status: http.StatusForbidden, Name: "visibility_unproved", RequiredPermission: "SUPERUSER_REQUIRED"}
	}
	return nil
}

func (c *Client) getUser(ctx context.Context, email string) (User, error) {
	records, err := c.listAll(ctx, c.gen.ListUsersApiV2AuthUsersGet, map[string]string{"email": "eq " + email})
	if err != nil {
		return User{}, err
	}
	for _, record := range records {
		if record.String("email") != email {
			return User{}, &ConflictError{Resource: "user", Reason: "email_mismatch"}
		}
	}
	record, err := selectOne("user", email, records)
	if err != nil {
		return User{}, err
	}
	return userFromRecord(record)
}

// GetUser requires global superuser visibility so absence and uniqueness are meaningful.
func (c *Client) GetUser(ctx context.Context, email string) (User, error) {
	if err := ValidateUserEmail(email); err != nil {
		return User{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	client, err := c.Resources()
	if err != nil {
		return User{}, err
	}
	if err := client.usersActor(ctx); err != nil {
		return User{}, err
	}
	return client.getUser(ctx, email)
}

// StatusPending means signup is still required; it is not completed access.
const StatusPending = "pending"

// EnsureSuperuser reconciles one exact email and explicit company UUID.
// Callers must serialize writers; the Backend offers no atomic ensure operation.
func (c *Client) EnsureSuperuser(ctx context.Context, email, companyID string, dryRun bool) (Result, error) {
	r := Result{Resource: "user"}
	if err := ValidateUserEmail(email); err != nil {
		return r, err
	}
	company, valid := resourceUUID(companyID)
	if !valid || company.String() != companyID {
		return r, errors.New("company must be a canonical nonzero UUID")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	client, err := c.Resources()
	if err != nil {
		return r, err
	}
	if err := client.usersActor(ctx); err != nil {
		return r, err
	}
	user, err := client.getUser(ctx, email)
	var missing *NotFoundError
	if err != nil && !errors.As(err, &missing) {
		return r, err
	}
	invitations, err := client.listAll(ctx, client.gen.ListUserInvitationsApiV2AuthUserInvitationsGet, map[string]string{"invitee_email": "eq " + email})
	if err != nil {
		return r, err
	}
	if missing == nil && (user.Company == nil || *user.Company != companyID) {
		return r, &ConflictError{Resource: "user", Reason: "company_mismatch", ID: user.ID}
	}
	activeInvitation, err := matchingUserInvitations(invitations, email, companyID, missing == nil && user.Status == "ACTIVE" && user.SignupCompletedAt != nil)
	if err != nil {
		return r, err
	}
	if missing == nil {
		r.ID, r.Details = user.ID, user.Project()
		if user.Status == "PENDING_ACTIVATION" && user.SignupCompletedAt == nil && user.IsSuperuser != nil && *user.IsSuperuser && activeInvitation != "" {
			r.Status = StatusPending
			return r, nil
		}
		if user.Status != "ACTIVE" {
			return r, &ConflictError{Resource: "user", Reason: "signup_reconciliation_required", ID: user.ID}
		}
		if user.SignupCompletedAt == nil {
			return r, &ConflictError{Resource: "user", Reason: "signup_completion_unproved", ID: user.ID}
		}
		if user.IsSuperuser == nil {
			return r, &ConflictError{Resource: "user", Reason: "superuser_state_unproved", ID: user.ID}
		}
		if activeInvitation != "" {
			return r, &ConflictError{Resource: "user", Reason: "invitation_reconciliation_required", ID: user.ID}
		}
		if *user.IsSuperuser {
			r.Status = StatusNoop
			return r, nil
		}
		if dryRun {
			r.Status = StatusPlanned
			r.Details["is_superuser"] = true
			return r, nil
		}
		id, _ := resourceUUID(user.ID)
		yes := true
		body, err := client.write(ctx, func(ctx context.Context) (*http.Response, error) {
			return client.gen.UpdateUserApiV2AuthUsersUserIdPatch(ctx, id, generated.UpdateUserRequest{IsSuperuser: &yes})
		})
		if err != nil {
			return r, err
		}
		record, err := dataRecord(body)
		if err != nil {
			return r, &UncertainError{Resource: "user"}
		}
		after, err := userFromRecord(record)
		if err != nil || after.ID != user.ID || after.Email != email || after.Company == nil || *after.Company != companyID || after.Status != "ACTIVE" || after.IsSuperuser == nil || !*after.IsSuperuser || after.SignupCompletedAt == nil {
			return r, &UncertainError{Resource: "user"}
		}
		r.Status, r.Details = StatusUpdated, after.Project()
		return r, nil
	}
	if len(invitations) != 0 {
		return r, &ConflictError{Resource: "user", Reason: "invitation_without_user"}
	}
	// Invitation creation resolves email OR username; a username collision must
	// not silently reuse or elevate an unrelated identity.
	byUsername, err := client.listAll(ctx, client.gen.ListUsersApiV2AuthUsersGet, map[string]string{"username": "eq " + email})
	if err != nil {
		return r, err
	}
	if len(byUsername) != 0 {
		return r, &ConflictError{Resource: "user", Reason: "username_in_use"}
	}
	yes := true
	r.Details = (User{Email: email, IsSuperuser: &yes, Status: "PENDING_ACTIVATION", Company: &companyID}).Project()
	if dryRun {
		r.Status = StatusPlanned
		return r, nil
	}
	body, err := client.write(ctx, func(ctx context.Context) (*http.Response, error) {
		return client.gen.SendSignupInvitationApiV2AuthActionSendSignupInvitationPost(ctx, generated.SendSignupInvitationRequest{
			CompanyId: company, InvitationType: generated.SIGNUPSUPERUSER, InviteeEmail: openapi_types.Email(email),
		})
	})
	if err != nil {
		// The user, invitation and email task are not one atomic transaction.
		var rejection *APIError
		if errors.As(err, &rejection) && rejection.Status >= 500 {
			return r, &UncertainError{Resource: "user invitation"}
		}
		return r, err
	}
	invitation, err := dataRecord(body)
	if err != nil {
		return r, &UncertainError{Resource: "user invitation"}
	}
	active, err := matchingUserInvitations([]Record{invitation}, email, companyID, false)
	if err != nil || active == "" {
		return r, &UncertainError{Resource: "user invitation"}
	}
	after, err := client.getUser(ctx, email)
	if err != nil || after.Company == nil || *after.Company != companyID || after.IsSuperuser == nil || !*after.IsSuperuser || after.Status != "PENDING_ACTIVATION" || after.SignupCompletedAt != nil {
		return r, &UncertainError{Resource: "user invitation"}
	}
	r.ID, r.Status, r.Details = after.ID, StatusPending, after.Project()
	return r, nil
}

// matchingUserInvitations inspects metadata only; tokens never enter results.
// Any conflicting history blocks creation, even when expired or already used.
func matchingUserInvitations(records []Record, email, companyID string, signupComplete bool) (string, error) {
	active := ""
	for _, invitation := range records {
		if _, valid := resourceUUID(invitation.ID()); !valid {
			return "", ErrInvalidResponse
		}
		if invitation.String("invitee_email") != email {
			return "", &ConflictError{Resource: "user invitation", Reason: "email_mismatch"}
		}
		company, valid := resourceUUID(invitation.String("company"))
		kind := generated.UserInvitationType(invitation.String("invitation_type"))
		if !valid || company.String() != companyID || !kind.Valid() {
			return "", &ConflictError{Resource: "user invitation", Reason: "invitation_conflict"}
		}
		expires, err := time.Parse(time.RFC3339Nano, invitation.String("expires_at"))
		if err != nil || expires.IsZero() {
			return "", ErrInvalidResponse
		}
		used, known := invitation["used_at"]
		if !known {
			return "", ErrInvalidResponse
		}
		if used != nil {
			if _, err := time.Parse(time.RFC3339Nano, invitation.String("used_at")); err != nil {
				return "", ErrInvalidResponse
			}
		}
		current := used == nil && expires.After(time.Now())
		if kind != generated.SIGNUPSUPERUSER && (!signupComplete || current) {
			return "", &ConflictError{Resource: "user invitation", Reason: "invitation_conflict"}
		}
		if current {
			if active != "" {
				return "", &ConflictError{Resource: "user invitation", Reason: "multiple_active_invitations"}
			}
			active = invitation.ID()
		}
	}
	return active, nil
}
