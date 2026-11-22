package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/policy"
)

// TestIsConflictMatchesTypedErrorOnly covers isConflict: only the Docker
// client's typed conflict error, wrapped or not, is a conflict. An error whose
// text says "conflict" is not (ADR-0017 decision 4).
func TestIsConflictMatchesTypedErrorOnly(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "the typed conflict error",
			err:  errdefs.ErrConflict,
			want: true,
		},
		{
			name: "a wrapped typed conflict error",
			err:  fmt.Errorf("remove image sha256:abc: %w", errdefs.ErrConflict),
			want: true,
		},
		{
			name: "an error whose text says conflict",
			err:  errors.New("image conflict: unable to delete"),
			want: false,
		},
		{
			name: "the typed not-found error",
			err:  errdefs.ErrNotFound,
			want: false,
		},
		{
			name: "a wrapped typed unavailable error whose text says conflict",
			err:  fmt.Errorf("conflict while removing: %w", errdefs.ErrUnavailable),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConflict(tc.err); got != tc.want {
				t.Errorf("isConflict(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestRemoveImageInUseIsNotAnError covers removeImage on an image a container
// still uses: the client returns its typed 409 conflict, which the code matches
// by type, so the removal is not an error and the image stays for the disk
// guard.
func TestRemoveImageInUseIsNotAnError(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	app := testAppID(p, "inuse")
	ref := "moonglass.local/" + p.name + "/web:inuse"
	p.registerImage(ref)
	id := importImage(t, e, ref, []string{"/app"})
	holder := createFixture(t, e, emptyPolicy(t, policy.Env{}),
		fixtureSpec(p, app, "inuse", ref, true), "mg-"+p.name+"-inuse")

	_, err := e.cli.ImageRemove(ctx, id, client.ImageRemoveOptions{})
	if err == nil {
		t.Fatalf("ImageRemove(%s) of an image container %s uses error = nil, want the client's typed HTTP 409 conflict", id, holder)
	}
	if !errdefs.IsConflict(err) {
		t.Fatalf("ImageRemove(%s) of an image container %s uses error = %v, want errdefs.IsConflict true: the client's typed HTTP 409 conflict", id, holder, err)
	}

	if err := e.removeImage(ctx, id); err != nil {
		t.Errorf("removeImage(%s) of an image container %s uses error = %v, want nil", id, holder, err)
	}
	if !stopImagePresent(t, e, id) {
		t.Errorf("image %s after removeImage = absent, want it still present while container %s uses it", id, holder)
	}
}

// TestRemoveImageReportsOtherFailures covers removeImage on a failure that is
// not the typed conflict or not found: it returns an error naming the image,
// and the image stays put.
func TestRemoveImageReportsOtherFailures(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	ref := "moonglass.local/" + p.name + "/web:other"
	p.registerImage(ref)
	id := importImage(t, e, ref, []string{"/app"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := e.removeImage(ctx, id)
	if err == nil {
		t.Fatalf("removeImage(%s) with a cancelled context error = nil, want an error naming the image", id)
	}
	if want := "remove image " + id; !strings.Contains(err.Error(), want) {
		t.Errorf("removeImage(%s) with a cancelled context error = %q, want it to contain %q", id, err, want)
	}
	if errdefs.IsConflict(err) {
		t.Errorf("removeImage(%s) with a cancelled context error = %v, want it not to be a typed conflict", id, err)
	}
	if !stopImagePresent(t, e, id) {
		t.Errorf("image %s after a failed removeImage = absent, want it still present", id)
	}
}
