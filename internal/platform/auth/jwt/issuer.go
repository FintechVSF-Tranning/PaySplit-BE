package jwt

import "time"

type Issuer struct{}

func NewIssuer(secret, issuer string, ttl time.Duration) (*Issuer, error) {
	panic("TODO: implement jwt.NewIssuer")
}

func (i *Issuer) Issue(userID int64) (string, int64, error) {
	panic("TODO: implement Issuer.Issue")
}
