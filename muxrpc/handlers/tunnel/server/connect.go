// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"go.cryptoscope.co/muxrpc/v2"

	"github.com/ssb-ngi-pointer/go-ssb-room/internal/network"
	refs "go.mindeco.de/ssb-refs"
)

type connectArg struct {
	Portal refs.FeedRef `json:"portal"` // the room server
	Target refs.FeedRef `json:"target"` // which peer the initiator/caller wants to be tunneld to
}

type connectWithOriginArg struct {
	connectArg
	Origin refs.FeedRef `json:"origin"` // who started the call
}

func (h *Handler) connect(ctx context.Context, req *muxrpc.Request, peerSrc *muxrpc.ByteSource, peerSnk *muxrpc.ByteSink) error {
	// unpack arguments
	var args []connectArg
	err := json.Unmarshal(req.RawArgs, &args)
	if err != nil {
		return fmt.Errorf("connect: invalid arguments: %w", err)
	}

	if n := len(args); n != 1 {
		return fmt.Errorf("connect: expected 1 argument, got %d", n)
	}
	arg := args[0]

	if !arg.Portal.Equal(&h.self) {
		return fmt.Errorf("talking to the wrong room")
	}

	// who made the call
	caller, err := network.GetFeedRefFromAddr(req.RemoteAddr())
	if err != nil {
		return err
	}

	// see if we have and endpoint for the target

	edp, has := h.state.Has(arg.Target)
	if !has {
		return fmt.Errorf("no such endpoint")
	}

	// call connect on them
	var argWorigin connectWithOriginArg
	argWorigin.connectArg = arg
	argWorigin.Origin = *caller

	targetSrc, targetSnk, err := edp.Duplex(ctx, muxrpc.TypeBinary, muxrpc.Method{"tunnel", "connect"}, argWorigin)
	if err != nil {
		return fmt.Errorf("failed to init connect call with target: %w", err)
	}

	// pipe data between caller and target
	var cpy muxrpcDuplexCopy
	cpy.logger = kitlog.With(h.logger, "caller", caller.ShortRef(), "target", arg.Target.ShortRef())
	cpy.ctx, cpy.cancel = context.WithCancel(ctx)

	go cpy.do(targetSnk, peerSrc)
	go cpy.do(peerSnk, targetSrc)

	return nil
}

type muxrpcDuplexCopy struct {
	ctx    context.Context
	cancel context.CancelFunc

	logger kitlog.Logger
}

func (mdc muxrpcDuplexCopy) do(w *muxrpc.ByteSink, r *muxrpc.ByteSource) {
	for r.Next(mdc.ctx) {
		err := r.Reader(func(rd io.Reader) error {
			_, err := io.Copy(w, rd)
			return err
		})
		if err != nil {
			level.Warn(mdc.logger).Log("event", "read failed", "err", err)
			w.CloseWithError(err)
			mdc.cancel()
			return
		}
	}
	if err := r.Err(); err != nil {
		level.Warn(mdc.logger).Log("event", "source errored", "err", err)
		// TODO: remove reading side from state?!
		w.CloseWithError(err)
		mdc.cancel()
	}

	return
}
