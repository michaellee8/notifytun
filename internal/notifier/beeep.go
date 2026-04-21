package notifier

import (
	"context"

	"github.com/gen2brain/beeep"
)

const beeepAppName = "notifytun"

var beeepNotify = beeep.Notify

type Beeep struct{}

func init() {
	beeep.AppName = beeepAppName
}

func NewBeeep() *Beeep {
	return &Beeep{}
}

func (b *Beeep) Notify(ctx context.Context, n Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return beeepNotify(n.Title, n.Body, "")
}
