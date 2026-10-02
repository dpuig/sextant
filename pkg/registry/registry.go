// Package registry maps typed API resources onto the tenant-scoped store. It
// owns validation and the rules for which fields clients may set: spec and
// labels come from users, status and resourceVersion are server-owned.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metavalidation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/dpuig/sextant/pkg/apis/v1alpha1"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
)

const maxLabels = 64

// ValidationError marks a client mistake (maps to HTTP 400/422).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{fmt.Sprintf(format, a...)} }

// Store is the persistence the registry needs; *storage.Store satisfies it.
type Store interface {
	Create(ctx context.Context, o storage.Object) (*storage.Object, error)
	Get(ctx context.Context, kind, name string) (*storage.Object, error)
	List(ctx context.Context, kind string) ([]*storage.Object, error)
	Update(ctx context.Context, o storage.Object) (*storage.Object, error)
	Delete(ctx context.Context, kind, name string) error
}

// Registry is the typed API over a Store.
type Registry struct{ store Store }

func New(s Store) *Registry { return &Registry{store: s} }

// Create validates and stores obj. Status and resourceVersion in obj are ignored.
func (r *Registry) Create(ctx context.Context, obj v1alpha1.Resource) (v1alpha1.Resource, error) {
	if err := checkCommon(ctx, obj); err != nil {
		return nil, err
	}
	o, err := encode(obj)
	if err != nil {
		return nil, err
	}
	o.Status = nil // server-owned; starts empty
	out, err := r.store.Create(ctx, o)
	if err != nil {
		return nil, err
	}
	return decode(obj.KindName(), out)
}

func (r *Registry) Get(ctx context.Context, kind, name string) (v1alpha1.Resource, error) {
	out, err := r.store.Get(ctx, kind, name)
	if err != nil {
		return nil, err
	}
	return decode(kind, out)
}

func (r *Registry) List(ctx context.Context, kind string) ([]v1alpha1.Resource, error) {
	objs, err := r.store.List(ctx, kind)
	if err != nil {
		return nil, err
	}
	res := make([]v1alpha1.Resource, 0, len(objs))
	for _, o := range objs {
		d, err := decode(kind, o)
		if err != nil {
			return nil, err
		}
		res = append(res, d)
	}
	return res, nil
}

// Update replaces spec and labels; the stored status is preserved. The caller's
// resourceVersion must be current.
func (r *Registry) Update(ctx context.Context, obj v1alpha1.Resource) (v1alpha1.Resource, error) {
	return r.update(ctx, obj, false)
}

// UpdateStatus replaces status only (for controllers and agents); spec and
// labels are preserved.
func (r *Registry) UpdateStatus(ctx context.Context, obj v1alpha1.Resource) (v1alpha1.Resource, error) {
	return r.update(ctx, obj, true)
}

func (r *Registry) update(ctx context.Context, obj v1alpha1.Resource, statusOnly bool) (v1alpha1.Resource, error) {
	// A status update carries no spec, so spec validation does not apply; the
	// stored spec was validated when it was written and is preserved below.
	if statusOnly {
		if !obj.HasStatus() {
			return nil, invalid("%s has no status", obj.KindName())
		}
	} else if err := checkCommon(ctx, obj); err != nil {
		return nil, err
	}
	rv, err := strconv.ParseInt(obj.GetResourceVersion(), 10, 64)
	if err != nil || rv <= 0 {
		return nil, invalid("metadata.resourceVersion is required for update")
	}
	cur, err := r.store.Get(ctx, obj.KindName(), obj.GetName())
	if err != nil {
		return nil, err
	}
	if cur.ResourceVersion != rv {
		return nil, storage.ErrConflict
	}
	in, err := encode(obj)
	if err != nil {
		return nil, err
	}
	next := *cur
	if statusOnly {
		next.Status = in.Status
	} else {
		next.Spec, next.Labels = in.Spec, in.Labels
	}
	out, err := r.store.Update(ctx, next) // carries cur.ResourceVersion: still checked atomically
	if err != nil {
		return nil, err
	}
	return decode(obj.KindName(), out)
}

func (r *Registry) Delete(ctx context.Context, kind, name string) error {
	return r.store.Delete(ctx, kind, name)
}

func checkCommon(ctx context.Context, obj v1alpha1.Resource) error {
	if err := obj.Validate(); err != nil {
		return &ValidationError{err.Error()}
	}
	if errs := metavalidation.ValidateLabels(obj.GetLabels(), field.NewPath("metadata", "labels")); len(errs) > 0 {
		return &ValidationError{errs.ToAggregate().Error()}
	}
	if len(obj.GetLabels()) > maxLabels {
		return invalid("metadata.labels: at most %d labels", maxLabels)
	}
	if obj.KindName() == "Organization" {
		// An Organization is the tenant itself: it lives inside its own tenant
		// and its name is the tenant ID.
		tid, err := tenancy.FromContext(ctx)
		if err != nil {
			return err
		}
		if obj.GetName() != tid.String() {
			return invalid("organization name %q must equal tenant %q", obj.GetName(), tid)
		}
	}
	return nil
}

func encode(obj v1alpha1.Resource) (storage.Object, error) {
	spec, err := json.Marshal(obj.SpecPtr())
	if err != nil {
		return storage.Object{}, err
	}
	status, err := json.Marshal(obj.StatusPtr())
	if err != nil {
		return storage.Object{}, err
	}
	return storage.Object{Kind: obj.KindName(), Name: obj.GetName(), Labels: obj.GetLabels(), Spec: spec, Status: status}, nil
}

func decode(kind string, o *storage.Object) (v1alpha1.Resource, error) {
	var info *v1alpha1.KindInfo
	for _, k := range v1alpha1.Kinds() {
		if k.Kind == kind {
			info = &k
			break
		}
	}
	if info == nil {
		return nil, fmt.Errorf("registry: unknown kind %q", kind)
	}
	r := info.New()
	if err := json.Unmarshal(o.Spec, r.SpecPtr()); err != nil {
		return nil, fmt.Errorf("registry: decode %s spec: %w", kind, err)
	}
	if err := json.Unmarshal(o.Status, r.StatusPtr()); err != nil {
		return nil, fmt.Errorf("registry: decode %s status: %w", kind, err)
	}
	r.SetName(o.Name)
	r.SetLabels(o.Labels)
	r.SetResourceVersion(strconv.FormatInt(o.ResourceVersion, 10))
	r.SetCreationTimestamp(metav1.NewTime(o.CreatedAt))
	r.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{Group: v1alpha1.GroupName, Version: v1alpha1.Version, Kind: kind})
	return r, nil
}
