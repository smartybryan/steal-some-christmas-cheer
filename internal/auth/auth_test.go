package auth

import (
	"testing"
	"time"

	"github.com/smarty/gunit"
	"github.com/smarty/gunit/assert/should"
)

func TestAuthFixture(t *testing.T) {
	gunit.Run(new(AuthFixture), t)
}

type AuthFixture struct {
	*gunit.Fixture
	signer Signer
	now    time.Time
}

func (this *AuthFixture) Setup() {
	this.signer = NewSigner([]byte("secret"))
	this.now = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
}

func (this *AuthFixture) TestOwnerTokenIsValidUntilExpiry() {
	token := this.signer.OwnerToken(this.now, time.Hour)
	this.So(this.signer.VerifyOwnerToken(token, this.now.Add(59*time.Minute)), should.BeTrue)
	this.So(this.signer.VerifyOwnerToken(token, this.now.Add(61*time.Minute)), should.BeFalse)
}

func (this *AuthFixture) TestTamperedOrForeignTokensAreRejected() {
	token := this.signer.OwnerToken(this.now, time.Hour)
	other := NewSigner([]byte("other")).OwnerToken(this.now, time.Hour)
	this.So(this.signer.VerifyOwnerToken(other, this.now), should.BeFalse)
	this.So(this.signer.VerifyOwnerToken(token+"0", this.now), should.BeFalse)
	this.So(this.signer.VerifyOwnerToken("garbage", this.now), should.BeFalse)
	this.So(this.signer.VerifyOwnerToken("", this.now), should.BeFalse)
}

func (this *AuthFixture) TestSignatureIsBoundToPurpose() {
	exp := this.now.Add(time.Hour).Unix()
	sig := this.signer.Sign("review:1", exp)
	this.So(this.signer.Verify("review:1", exp, sig, this.now), should.BeTrue)
	this.So(this.signer.Verify("review:2", exp, sig, this.now), should.BeFalse)
	this.So(this.signer.Verify("review:1", exp+1, sig, this.now), should.BeFalse)
}

func (this *AuthFixture) TestPasswordMatches() {
	this.So(PasswordMatches("ho ho ho", "ho ho ho"), should.BeTrue)
	this.So(PasswordMatches("ho ho", "ho ho ho"), should.BeFalse)
}
