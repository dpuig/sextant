// Package v1alpha1 defines the Sextant platform API objects.
package v1alpha1

import (
	"errors"
	"fmt"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	GroupName = "sextant.andean.io"
	Version   = "v1alpha1"
)

// Resource is implemented by every API object. SpecPtr and StatusPtr expose
// the typed spec/status so the registry can persist them without reflection.
type Resource interface {
	metav1.Object
	GetObjectKind() schema.ObjectKind
	KindName() string
	SpecPtr() any
	StatusPtr() any
	// HasStatus reports whether the kind has a status subresource.
	HasStatus() bool
	Validate() error
}

// KindInfo describes a registered kind.
type KindInfo struct {
	Kind   string
	Plural string
	New    func() Resource
}

// Kinds lists every API kind. Organization is the tenant root and is routed
// specially by the server; the rest are tenant-scoped collections.
func Kinds() []KindInfo {
	return []KindInfo{
		{"Organization", "organizations", func() Resource { return &Organization{} }},
		{"Workspace", "workspaces", func() Resource { return &Workspace{} }},
		{"Environment", "environments", func() Resource { return &Environment{} }},
		{"Cluster", "clusters", func() Resource { return &Cluster{} }},
		{"AccessGrant", "accessgrants", func() Resource { return &AccessGrant{} }},
	}
}

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func validName(n string) error {
	if !namePattern.MatchString(n) {
		return fmt.Errorf("name %q invalid: must match %s", n, namePattern)
	}
	return nil
}

func required(field, v string) error {
	if v == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

// --- Organization (the tenant) ---

type Organization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              OrganizationSpec `json:"spec,omitempty"`
}
type OrganizationSpec struct {
	DisplayName string `json:"displayName,omitempty"`
}

func (o *Organization) KindName() string { return "Organization" }
func (o *Organization) SpecPtr() any     { return &o.Spec }
func (o *Organization) StatusPtr() any   { return &struct{}{} }
func (o *Organization) HasStatus() bool  { return false }
func (o *Organization) Validate() error  { return validName(o.Name) }

// --- Workspace ---

type Workspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              WorkspaceSpec `json:"spec,omitempty"`
}
type WorkspaceSpec struct {
	DisplayName string `json:"displayName,omitempty"`
}

func (o *Workspace) KindName() string { return "Workspace" }
func (o *Workspace) SpecPtr() any     { return &o.Spec }
func (o *Workspace) StatusPtr() any   { return &struct{}{} }
func (o *Workspace) HasStatus() bool  { return false }
func (o *Workspace) Validate() error  { return validName(o.Name) }

// --- Environment ---

type Environment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              EnvironmentSpec `json:"spec,omitempty"`
}
type EnvironmentSpec struct {
	Workspace   string `json:"workspace"`
	Criticality string `json:"criticality,omitempty"` // low | medium | high
}

func (o *Environment) KindName() string { return "Environment" }
func (o *Environment) SpecPtr() any     { return &o.Spec }
func (o *Environment) StatusPtr() any   { return &struct{}{} }
func (o *Environment) HasStatus() bool  { return false }
func (o *Environment) Validate() error {
	if err := validName(o.Name); err != nil {
		return err
	}
	if err := required("spec.workspace", o.Spec.Workspace); err != nil {
		return err
	}
	switch o.Spec.Criticality {
	case "", "low", "medium", "high":
		return nil
	}
	return fmt.Errorf("spec.criticality %q invalid: want low, medium or high", o.Spec.Criticality)
}

// --- Cluster ---

type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ClusterSpec   `json:"spec,omitempty"`
	Status            ClusterStatus `json:"status,omitempty"`
}
type ClusterSpec struct {
	Environment string `json:"environment"`
	Provider    string `json:"provider,omitempty"`
}
type ClusterStatus struct {
	Connected bool         `json:"connected"`
	LastSeen  *metav1.Time `json:"lastSeen,omitempty"`
}

func (o *Cluster) KindName() string { return "Cluster" }
func (o *Cluster) SpecPtr() any     { return &o.Spec }
func (o *Cluster) StatusPtr() any   { return &o.Status }
func (o *Cluster) HasStatus() bool  { return true }
func (o *Cluster) Validate() error {
	if err := validName(o.Name); err != nil {
		return err
	}
	return required("spec.environment", o.Spec.Environment)
}

// --- AccessGrant ---

type AccessGrant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AccessGrantSpec `json:"spec,omitempty"`
}
type AccessGrantSpec struct {
	Subject   string       `json:"subject"`
	Cluster   string       `json:"cluster"`
	Role      string       `json:"role"` // view | edit | admin
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

func (o *AccessGrant) KindName() string { return "AccessGrant" }
func (o *AccessGrant) SpecPtr() any     { return &o.Spec }
func (o *AccessGrant) StatusPtr() any   { return &struct{}{} }
func (o *AccessGrant) HasStatus() bool  { return false }
func (o *AccessGrant) Validate() error {
	if err := validName(o.Name); err != nil {
		return err
	}
	if err := errors.Join(required("spec.subject", o.Spec.Subject), required("spec.cluster", o.Spec.Cluster)); err != nil {
		return err
	}
	switch o.Spec.Role {
	case "view", "edit", "admin":
		return nil
	}
	return fmt.Errorf("spec.role %q invalid: want view, edit or admin", o.Spec.Role)
}
