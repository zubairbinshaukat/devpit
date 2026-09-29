package elevate

import (
	"context"
	"errors"
)

// Share asks the worker to run one file sharing operation and blocks until it
// finishes. onLine, if not nil, receives the operation's progress lines in
// order, including the FW=<rule> and SID=<sid> lines a caller records for its
// manifest. A refusal or a failure comes back as an error whose text is a
// sentence.
//
// The request's password is sent once over the local pipe and is not kept by
// the client.
func (c *Client) Share(ctx context.Context, req ShareRequest, onLine func(text string)) error {
	id := newID()
	ch := c.register(id)
	defer c.unregister(id)

	if err := c.send(Request{ID: id, Kind: KindShare, Share: &req}); err != nil {
		return err
	}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return c.deathError()
			}
			switch ev.Event {
			case EventLine:
				if onLine != nil {
					onLine(ev.Text)
				}
			case EventDone:
				if ev.Err != "" {
					return errors.New(ev.Err)
				}
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
