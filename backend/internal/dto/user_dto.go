package dto

import "time"

// UserProfileResponse – clean, safe user data
type UserProfileResponse struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Email        string                    `json:"email"`
	Phone        string                    `json:"phone,omitempty"`
	Status       string                    `json:"status"`
	JoiningDate  time.Time                 `json:"joining_date"`
	LastLoginAt  *time.Time                `json:"last_login_at,omitempty"`
	Organization *OrganizationBrief        `json:"organization,omitempty"`
	Branch       *BranchBrief              `json:"branch,omitempty"`
	Role         *RoleWithPermissionsBrief `json:"role"`
}

type OrganizationBrief struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	City    string `json:"city,omitempty"`
	Country string `json:"country,omitempty"`
	LogoURL string `json:"logo_url,omitempty"`
}

type BranchBrief struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

type RoleWithPermissionsBrief struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Permissions []PermissionBrief `json:"permissions"`
}

type PermissionBrief struct {
	Module string `json:"module"`
	Action string `json:"action"`
}
