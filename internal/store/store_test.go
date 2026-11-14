package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/smarty/gunit"
	"github.com/smarty/gunit/assert/should"
)

func TestStoreFixture(t *testing.T) {
	gunit.Run(new(StoreFixture), t)
}

type StoreFixture struct {
	*gunit.Fixture
	store *Store
	ctx   context.Context
}

func (this *StoreFixture) Setup() {
	st, err := Open(filepath.Join(this.T().(*testing.T).TempDir(), "duck.db"))
	this.So(err, should.BeNil)
	this.store = st
	this.ctx = context.Background()
}

func (this *StoreFixture) Teardown() {
	_ = this.store.Close()
}

func (this *StoreFixture) add(name string, status Status) int64 {
	id, err := this.store.Create(this.ctx, Registration{
		CreatedAt: time.Now(), Name: name, Street: "1 Elm St", City: "Town", PublicLocation: "Elm St, Town",
	}, []byte("jpeg"))
	this.So(err, should.BeNil)
	if status != StatusPending {
		this.So(this.store.Review(this.ctx, id, status, name, "Elm St, Town"), should.BeNil)
	}
	return id
}

func (this *StoreFixture) names(trail []Registration) []string {
	var out []string
	for _, r := range trail {
		out = append(out, r.Name)
	}
	return out
}

func (this *StoreFixture) TestEmptyTrail() {
	trail, total, err := this.store.PublicTrail(this.ctx, false)
	this.So(err, should.BeNil)
	this.So(trail, should.BeEmpty)
	this.So(total, should.Equal, 0)
}

func (this *StoreFixture) TestNewestRegistrationIsHiddenEvenWhenApproved() {
	this.add("first", StatusApproved)
	this.add("second", StatusApproved)

	trail, total, err := this.store.PublicTrail(this.ctx, false)
	this.So(err, should.BeNil)
	this.So(this.names(trail), should.Equal, []string{"first"})
	this.So(total, should.Equal, 2)
}

func (this *StoreFixture) TestPendingNewestStillHidesItselfAndRevealsPrevious() {
	this.add("first", StatusApproved)
	this.add("second", StatusPending)

	trail, _, err := this.store.PublicTrail(this.ctx, false)
	this.So(err, should.BeNil)
	this.So(this.names(trail), should.Equal, []string{"first"})
}

func (this *StoreFixture) TestPendingEntriesAreNeverPublic() {
	this.add("first", StatusPending)
	this.add("second", StatusApproved)
	this.add("third", StatusApproved)

	trail, _, err := this.store.PublicTrail(this.ctx, false)
	this.So(err, should.BeNil)
	this.So(this.names(trail), should.Equal, []string{"second"})
}

func (this *StoreFixture) TestPhotoRejectedEntriesArePublicWithoutPhoto() {
	first := this.add("first", StatusPhotoRejected)
	this.add("second", StatusApproved)

	trail, _, err := this.store.PublicTrail(this.ctx, false)
	this.So(err, should.BeNil)
	this.So(this.names(trail), should.Equal, []string{"first"})

	_, err = this.store.PublicPhoto(this.ctx, first, false)
	this.So(errors.Is(err, ErrNotFound), should.BeTrue)
}

func (this *StoreFixture) TestRevealAllShowsNewest() {
	this.add("first", StatusApproved)
	last := this.add("second", StatusApproved)

	trail, _, err := this.store.PublicTrail(this.ctx, true)
	this.So(err, should.BeNil)
	this.So(this.names(trail), should.Equal, []string{"first", "second"})

	photo, err := this.store.PublicPhoto(this.ctx, last, true)
	this.So(err, should.BeNil)
	this.So(string(photo), should.Equal, "jpeg")
}

func (this *StoreFixture) TestNewestPhotoIsNotPublic() {
	first := this.add("first", StatusApproved)
	last := this.add("second", StatusApproved)

	_, err := this.store.PublicPhoto(this.ctx, last, false)
	this.So(errors.Is(err, ErrNotFound), should.BeTrue)

	photo, err := this.store.PublicPhoto(this.ctx, first, false)
	this.So(err, should.BeNil)
	this.So(string(photo), should.Equal, "jpeg")
}

func (this *StoreFixture) TestDeletingSpamRevealsNothingNew() {
	this.add("first", StatusApproved)
	this.add("holder", StatusApproved)
	spam := this.add("spam", StatusPending)

	this.So(this.store.Delete(this.ctx, spam), should.BeNil)

	trail, _, err := this.store.PublicTrail(this.ctx, false)
	this.So(err, should.BeNil)
	this.So(this.names(trail), should.Equal, []string{"first"})
}

func (this *StoreFixture) TestSettingsRoundTrip() {
	empty, err := this.store.Settings(this.ctx)
	this.So(err, should.BeNil)
	this.So(empty, should.Equal, Settings{})

	want := Settings{OwnerName: "The Smiths", ReturnAddress: "1 Main\nTown", RevealAll: true}
	this.So(this.store.SaveSettings(this.ctx, want), should.BeNil)
	got, err := this.store.Settings(this.ctx)
	this.So(err, should.BeNil)
	this.So(got, should.Equal, want)
}
