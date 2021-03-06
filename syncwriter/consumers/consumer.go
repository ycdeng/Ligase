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

package consumers

import (
	"context"
	jsonRaw "encoding/json"
	"fmt"

	"github.com/finogeeks/ligase/common"
	"github.com/finogeeks/ligase/common/config"
	"github.com/finogeeks/ligase/core"
	"github.com/finogeeks/ligase/model/repos"
	"github.com/finogeeks/ligase/model/roomservertypes"
	"github.com/finogeeks/ligase/model/service/roomserverapi"
	"github.com/finogeeks/ligase/model/syncapitypes"
	"github.com/finogeeks/ligase/model/types"
	"github.com/finogeeks/ligase/plugins/message/external"
	"github.com/finogeeks/ligase/skunkworks/gomatrixserverlib"
	"github.com/finogeeks/ligase/skunkworks/log"
	"github.com/finogeeks/ligase/storage/model"
	"github.com/finogeeks/ligase/syncserver/extra"
	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// OutputRoomEventConsumer consumes events that originated in the room server.
type RoomEventConsumer struct {
	channel             core.IChannel
	db                  model.SyncAPIDatabase
	roomStateTimeLine   *repos.RoomStateTimeLineRepo
	roomHistoryTimeLine *repos.RoomHistoryTimeLineRepo
	roomCurState        *repos.RoomCurStateRepo
	displayNameRepo     *repos.DisplayNameRepo
	chanSize            uint32
	//msgChan             []chan *roomserverapi.OutputNewRoomEvent
	msgChan []chan common.ContextMsg
	//backFillChan        []chan *roomserverapi.OutputNewRoomEvent
	backFillChan []chan common.ContextMsg
	cfg          *config.Dendrite
}

func NewRoomEventConsumer(
	cfg *config.Dendrite,
	store model.SyncAPIDatabase,
) *RoomEventConsumer {
	val, ok := common.GetTransportMultiplexer().GetChannel(
		cfg.Kafka.Consumer.OutputRoomEventSyncWriter.Underlying,
		cfg.Kafka.Consumer.OutputRoomEventSyncWriter.Name,
	)
	if ok {
		channel := val.(core.IChannel)
		s := &RoomEventConsumer{
			channel:  channel,
			db:       store,
			chanSize: 16,
			cfg:      cfg,
		}
		channel.SetHandler(s)

		return s
	}

	return nil
}

func (s *RoomEventConsumer) SetRoomHistory(roomHistory *repos.RoomHistoryTimeLineRepo) *RoomEventConsumer {
	s.roomHistoryTimeLine = roomHistory
	return s
}

func (s *RoomEventConsumer) SetRsTimeline(rsTimeline *repos.RoomStateTimeLineRepo) *RoomEventConsumer {
	s.roomStateTimeLine = rsTimeline
	return s
}

func (s *RoomEventConsumer) SetRsCurState(rsCurState *repos.RoomCurStateRepo) *RoomEventConsumer {
	s.roomCurState = rsCurState
	return s
}

func (s *RoomEventConsumer) SetDisplayNameRepo(displayNameRepo *repos.DisplayNameRepo) *RoomEventConsumer {
	s.displayNameRepo = displayNameRepo
	return s
}

func (s *RoomEventConsumer) startWorker(msgChan chan common.ContextMsg) {
	for msg := range msgChan {
		data := msg.Msg.(*roomserverapi.OutputNewRoomEvent)
		s.onNewRoomEvent(msg.Ctx, data)
	}
}

func (s *RoomEventConsumer) startBackFill(msgChan chan common.ContextMsg) {
	for msg := range msgChan {
		data := msg.Msg.(*roomserverapi.OutputNewRoomEvent)
		s.onBackFillEvent(msg.Ctx, data)
	}
}

func (s *RoomEventConsumer) Start() error {
	s.msgChan = make([]chan common.ContextMsg, s.chanSize)
	for i := uint32(0); i < s.chanSize; i++ {
		s.msgChan[i] = make(chan common.ContextMsg, 512)
		go s.startWorker(s.msgChan[i])
	}

	s.backFillChan = make([]chan common.ContextMsg, s.chanSize)
	for i := uint32(0); i < s.chanSize; i++ {
		s.backFillChan[i] = make(chan common.ContextMsg, 512)
		go s.startBackFill(s.backFillChan[i])
	}
	//s.channel.Start()
	return nil
}

func (s *RoomEventConsumer) OnMessage(ctx context.Context, topic string, partition int32, data []byte, rawMsg interface{}) {
	var output roomserverapi.OutputEvent
	if err := json.Unmarshal(data, &output); err != nil {
		log.Errorw("sync writer: message parse failure", log.KeysAndValues{"error", err})
		return
	}

	log.Debugw("sync writer received data", log.KeysAndValues{"type", output.Type, "topic", topic})

	switch output.Type {
	case roomserverapi.OutputTypeNewRoomEvent:
		if common.IsRelatedRequest(output.NewRoomEvent.Event.RoomID, s.cfg.MultiInstance.Instance, s.cfg.MultiInstance.Total, s.cfg.MultiInstance.MultiWrite) {
			log.Infow("sync writer received event from room server", log.KeysAndValues{"type", output.NewRoomEvent.Event.Type, "event_id", output.NewRoomEvent.Event.EventID, "room_id", output.NewRoomEvent.Event.RoomID})
			idx := common.CalcStringHashCode(output.NewRoomEvent.Event.RoomID) % s.chanSize
			s.msgChan[idx] <- common.ContextMsg{Ctx: ctx, Msg: output.NewRoomEvent}
		}
	case roomserverapi.OutputBackfillRoomEvent:
		if common.IsRelatedRequest(output.NewRoomEvent.Event.RoomID, s.cfg.MultiInstance.Instance, s.cfg.MultiInstance.Total, s.cfg.MultiInstance.MultiWrite) {
			log.Infow("sync writer received back fill event from room server", log.KeysAndValues{"type", output.NewRoomEvent.Event.Type, "event_id", output.NewRoomEvent.Event.EventID, "room_id", output.NewRoomEvent.Event.RoomID})
			idx := common.CalcStringHashCode(output.NewRoomEvent.Event.RoomID) % s.chanSize
			s.backFillChan[idx] <- common.ContextMsg{Ctx: ctx, Msg: output.NewRoomEvent}
		}
	default:
		log.Debugw("sync writer: ignoring unknown output type", log.KeysAndValues{"type", output.Type})
	}
}

func (s *RoomEventConsumer) processStateEv(ev *gomatrixserverlib.ClientEvent) (gomatrixserverlib.ClientEvent, error) {
	rs := s.roomCurState.GetRoomState(ev.RoomID)

	if rs != nil {
		stateKey := ""
		if ev.StateKey != nil {
			stateKey = *ev.StateKey
		}
		pre := rs.GetState(ev.Type, stateKey)
		if pre != nil {
			prev := syncapitypes.PrevEventRef{
				PrevContent:   jsonRaw.RawMessage(pre.GetEv().Content),
				ReplacesState: pre.GetEv().EventID,
				PrevSender:    pre.GetEv().Sender,
				PreOffset:     pre.GetOffset(),
			}

			prevBytes, err := jsonRaw.Marshal(prev)
			if err != nil {
				return *ev, err
			}

			ev.Unsigned = prevBytes
		}
	}

	return *ev, nil
}

func (s *RoomEventConsumer) processRedactEv(ctx context.Context, ev *gomatrixserverlib.ClientEvent) {
	var redactEv gomatrixserverlib.ClientEvent

	stream := s.roomHistoryTimeLine.GetStreamEv(ctx, ev.RoomID, ev.Redacts)
	if stream != nil {
		redactEv = *stream.Ev
		log.Infof("processRedactEv get redact:%s ev:%v from timeline", ev.Redacts, redactEv)
	} else {
		evs, err := s.db.Events(ctx, []string{ev.Redacts})
		if err == nil && len(evs) > 0 {
			redactEv = evs[0]
			log.Infof("processRedactEv get redact:%s ev:%v from db", ev.Redacts, redactEv)
		} else {
			log.Errorf("processRedactEv cannot found redact:%s ev:%v both timeline and db", ev.Redacts, redactEv)
			return
		}
	}

	unsigned := types.RedactUnsigned{}
	if ev.Type == "m.room.redaction" {
		content := map[string]interface{}{}
		empty, _ := json.Marshal(content)
		redactEv.Content = empty
		redactEv.Hint = fmt.Sprintf("%s撤回了一条消息", extra.GetDisplayName(s.displayNameRepo, ev.Sender))
		unsigned.RedactedBecause = ev
	} else {
		redactEv.Content = ev.Content
		unsigned.UpdatedBecause = ev
	}
	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		log.Errorf("processRedactEv redact Marshal:%s evs:%v, err:%v", ev.Redacts, unsigned, err)
		return
	}
	redactEv.Unsigned = unsignedBytes
	if stream != nil {
		stream.Ev = &redactEv //更新timeline
	}
	if err := s.db.UpdateEvent(ctx, redactEv, ev.Redacts, redactEv.Type, ev.RoomID); err != nil {
		log.Errorf("processRedactEv update redact:%s ev:%v to db err:%v", ev.Redacts, redactEv, err)
	} else {
		log.Infof("processRedactEv update redact:%s ev:%v to db succ", ev.Redacts, redactEv)
	}
}

func (s *RoomEventConsumer) onNewRoomEvent(
	ctx context.Context, msg *roomserverapi.OutputNewRoomEvent,
) error {
	ev := msg.Event
	domain, _ := common.DomainFromID(ev.Sender)
	if ev.Type != "m.room.create" {
		s.roomStateTimeLine.GetStateStreams(ctx, ev.RoomID) //load state stream timeline& state
		s.roomHistoryTimeLine.LoadHistory(ctx, ev.RoomID, true)
		preOffset := s.roomHistoryTimeLine.GetDomainMaxStream(ctx, ev.RoomID, domain)
		if preOffset != -1 && preOffset+1 != ev.DomainOffset {
			s.db.InsertOutputMinStream(ctx, ev.EventOffset, ev.RoomID)
		}
	}
	s.roomHistoryTimeLine.SetDomainMaxStream(ev.RoomID, domain, ev.DomainOffset)

	if common.IsStateClientEv(&ev) == true { //state ev
		ev, _ = s.processStateEv(&ev)
	} else if ev.Type == "m.room.redaction" || ev.Type == "m.room.update" {
		s.processRedactEv(ctx, &ev)
	}

	transId := ""
	if msg.TransactionID != nil {
		transId = msg.TransactionID.TransactionID
	}

	if (ev.Type == "m.room.message" || ev.Type == "m.room.encrypted") && transId != "" {
		unsigned := types.Unsigned{}
		unsigned.TransactionID = transId
		unsignedBytes, err := json.Marshal(unsigned)
		if err != nil {
			log.Errorf("RoomEventFeedConsumer.onNewRoomEvent Marshal:%s evs:%v, err:%v", ev.Redacts, unsigned, err)
		}
		ev.Unsigned = unsignedBytes
	}

	if ev.StateKey != nil {
		msg.TransactionID = &roomservertypes.TransactionID{
			DeviceID:      *ev.StateKey,
			TransactionID: transId,
		}
	}

	err := s.db.WriteEvent(ctx, &ev, []gomatrixserverlib.ClientEvent{}, msg.AddsStateEventIDs, msg.RemovesStateEventIDs, msg.TransactionID, ev.EventOffset, ev.DomainOffset, ev.Depth, domain, int64(ev.OriginServerTS))
	if err != nil {
		log.Errorw("syncwriter: write event failure", log.KeysAndValues{"event_id", string(ev.EventID), "error", err, "add", msg.AddsStateEventIDs, "del", msg.RemovesStateEventIDs})
		return err
	}

	membership := ""
	if common.IsStateClientEv(&ev) {
		if ev.Type == "m.room.member" {
			con := external.MemberContent{}
			json.Unmarshal(ev.Content, &con)
			membership = con.Membership
		}

		err = s.db.UpdateRoomState(ctx, ev, &membership, syncapitypes.StreamPosition(ev.EventOffset))
		if err != nil {
			log.Errorw("syncwriter: UpdateRoomState failure", log.KeysAndValues{"event_id", string(ev.EventID), "error", err, "add", msg.AddsStateEventIDs, "del", msg.RemovesStateEventIDs})
			return err
		}

		s.roomStateTimeLine.AddStreamEv(ctx, &ev, ev.EventOffset, true) //保留state stream
	}

	s.roomHistoryTimeLine.AddEv(ctx, &ev, ev.EventOffset, true) //更新room timeline

	return nil
}

func (s *RoomEventConsumer) onBackFillEvent(
	ctx context.Context, msg *roomserverapi.OutputNewRoomEvent,
) error {
	ev := msg.Event
	domain, _ := common.DomainFromID(ev.Sender)

	preOffset := s.roomHistoryTimeLine.GetDomainMaxStream(ctx, ev.RoomID, domain)
	if preOffset <= ev.DomainOffset {
		s.db.InsertOutputMinStream(ctx, ev.EventOffset, ev.RoomID)
	}

	if common.IsStateClientEv(&ev) == true { //state ev
		ev, _ = s.processStateEv(&ev)
	} else if ev.Type == "m.room.redaction" || ev.Type == "m.room.update" {
		s.processRedactEv(ctx, &ev)
	}
	err := s.db.WriteEvent(ctx, &ev, []gomatrixserverlib.ClientEvent{}, msg.AddsStateEventIDs, msg.RemovesStateEventIDs, msg.TransactionID, -ev.EventOffset, ev.DomainOffset, ev.Depth, domain, int64(ev.OriginServerTS))
	if err != nil {
		log.Errorw("syncwriter: write event failure", log.KeysAndValues{"event_id", string(ev.EventID), "error", err, "add", msg.AddsStateEventIDs, "del", msg.RemovesStateEventIDs})
		return err
	}

	return nil
}
