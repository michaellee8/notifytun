package notifier

import (
	"context"

	"github.com/gen2brain/beeep"
)

const beeepAppName = "notifytun"

var beeepNotify = beeep.Notify

type Beeep struct{}

func NewBeeep() *Beeep {
	return &Beeep{}
}

func (b *Beeep) Notify(ctx context.Context, n Notification) error {
	_ = ctx

	beeep.AppName = beeepAppName
	return beeepNotify(n.Title, n.Body, "")
}
