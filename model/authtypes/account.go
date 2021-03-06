// Copyright 2017 Vector Creations Ltd
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
//
// Modifications copyright (C) 2020 Finogeeks Co., Ltd

package authtypes

import (
	"github.com/finogeeks/ligase/skunkworks/gomatrixserverlib"
)

// Account represents a Matrix account on this home server.
type Account struct {
	UserID       string
	ServerName   gomatrixserverlib.ServerName
	Profile      *Profile
	AppServiceID string
	// TODO: Other flags like IsAdmin, IsGuest
	// TODO: Devices
	// TODO: Associations (e.g. with application services)
}

type RoomTagCacheData struct {
	UserID  string
	RoomID  string
	Tag     string
	Content string
}

type AccountDataCacheData struct {
	UserID  string
	Type    string
	Content string
}

type RoomAccountDataCacheData struct {
	UserID  string
	RoomID  string
	Type    string
	Content string
}

type RoomTags struct {
	Tags map[string]interface{} `json:"tags"`
}
