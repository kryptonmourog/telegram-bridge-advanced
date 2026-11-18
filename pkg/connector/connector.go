// mautrix-telegram - A Matrix-Telegram puppeting bridge.
// Copyright (C) 2024 Sumner Evans
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package connector

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/matrix"

	"go.mau.fi/mautrix-telegram/pkg/connector/forkdb"
	"go.mau.fi/mautrix-telegram/pkg/connector/store"
)

type TelegramConnector struct {
	Bridge *bridgev2.Bridge
	Config TelegramConfig
	Store  *store.Container
	// forkDB runs the migrations of the fork's own tables (see forkdb); queries go through Store.
	forkDB *dbutil.Database

	useDirectMedia bool
	maxFileSize    int64
	// channelKinds caches whether channel IDs are broadcast channels (see channelkind.go).
	channelKinds sync.Map
}

var _ bridgev2.NetworkConnector = (*TelegramConnector)(nil)
var _ bridgev2.MaxFileSizeingNetwork = (*TelegramConnector)(nil)

func (tc *TelegramConnector) Init(bridge *bridgev2.Bridge) {
	tc.Store = store.NewStore(bridge.DB.Database, dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "telegram").Logger()))
	tc.forkDB = bridge.DB.Database.Child("fork_version", forkdb.Table, dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "fork").Logger()))
	tc.Bridge = bridge
	tc.Bridge.Commands.(*commands.Processor).AddHandlers(cmdSyncChats, cmdEmojiPack, cmdUpgrade, cmdJoin, cmdFolders, cmdArchive, cmdAds, cmdLeftChats, cmdLang, cmdSettings, cmdEncryption)
}

func (tc *TelegramConnector) Start(ctx context.Context) error {
	if err := tc.applyMatrixRequestTimeout(); err != nil {
		return err
	}
	if tc.useDirectMedia {
		tc.Bridge.Log.Info().Str("scope", tc.Config.GetDirectMediaScope()).Msg("Enabled direct media")
	}
	if err := tc.Store.Upgrade(ctx); err != nil {
		return err
	}
	if err := tc.forkDB.Upgrade(ctx); err != nil {
		return fmt.Errorf("failed to upgrade the fork's tables: %w", err)
	}
	tc.registerAdsRoomReceipts()
	tc.installTranslator()
	return nil
}

// matrixResponseTimeout is how long to wait for the homeserver's response once a request has been
// sent, when matrix_request_timeout is set. Synapse answers an upload only after writing the whole
// file to its media store, which took over 3 minutes for a 1.4 GB file on a busy server; the upload
// was then cancelled and sent again forever.
const matrixResponseTimeout = 15 * time.Minute

// applyMatrixRequestTimeout replaces the total timeout of the appservice HTTP client, which every
// Matrix client of the bridge shares, with matrix_request_timeout, and limits the wait for response
// headers instead. That lets large uploads take as long as the link needs.
func (tc *TelegramConnector) applyMatrixRequestTimeout() error {
	if tc.Config.MatrixRequestTimeout == "" {
		return nil
	}
	timeout, err := time.ParseDuration(tc.Config.MatrixRequestTimeout)
	if err != nil {
		return fmt.Errorf("invalid matrix_request_timeout: %w", err)
	}
	mc, ok := tc.Bridge.Matrix.(*matrix.Connector)
	if !ok || mc.AS == nil || mc.AS.HTTPClient == nil {
		return nil
	}
	client := mc.AS.HTTPClient
	if client.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = matrixResponseTimeout
		client.Transport = transport
	} else if transport, ok := client.Transport.(*http.Transport); ok {
		transport.ResponseHeaderTimeout = matrixResponseTimeout
	}
	client.Timeout = timeout
	tc.Bridge.Log.Info().Stringer("timeout", timeout).Msg("Set total timeout of homeserver requests")
	return nil
}

func (tc *TelegramConnector) PreSplitPortalMigrate(ctx context.Context) error {
	return tc.Store.Upgrade(ctx)
}

func (tc *TelegramConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) (err error) {
	login.Client, err = NewTelegramClient(ctx, tc, login)
	return
}

func (tc *TelegramConnector) SetMaxFileSize(maxSize int64) {
	tc.maxFileSize = maxSize
}

func (tc *TelegramConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "Telegram",
		NetworkURL:           "https://telegram.org/",
		NetworkIcon:          "mxc://maunium.net/tJCRmUyJDsgRNgqhOgoiHWbX",
		NetworkID:            "telegram",
		BeeperBridgeType:     "telegram",
		DefaultPort:          29317,
		DefaultCommandPrefix: "!tg",
	}
}
