// mautrix-telegram - A Matrix-Telegram puppeting bridge.
// Copyright (C) 2026 Sumner Evans
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package connector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
)

func TestDirectMediaScope(t *testing.T) {
	channel := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: "channel:1"}}}
	dm := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: "user:1"}}}
	basicGroup := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: "chat:1"}}}

	tests := []struct {
		name        string
		scope       string
		available   bool
		channel     bool
		dm          bool
		basicGroup  bool
		globalMedia bool
	}{
		{name: "default", available: true, channel: true},
		{name: "channels", scope: DirectMediaScopeChannels, available: true, channel: true},
		{name: "all", scope: DirectMediaScopeAll, available: true, channel: true, dm: true, basicGroup: true, globalMedia: true},
		{name: "off", scope: DirectMediaScopeOff, available: true},
		{name: "proxy unavailable", scope: DirectMediaScopeAll},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := &TelegramConnector{Config: TelegramConfig{DirectMediaScope: tt.scope}, useDirectMedia: tt.available}
			assert.Equal(t, tt.channel, tc.shouldUseDirectMediaForPortal(channel))
			assert.Equal(t, tt.dm, tc.shouldUseDirectMediaForPortal(dm))
			assert.Equal(t, tt.basicGroup, tc.shouldUseDirectMediaForPortal(basicGroup))
			assert.Equal(t, tt.globalMedia, tc.shouldUseDirectMediaForGlobalMedia())
			assert.Equal(t, tt.channel, tc.shouldUseDirectMediaForPeerType(ids.PeerTypeChannel))
		})
	}
}
