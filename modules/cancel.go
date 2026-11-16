package modules

import (
	"context"
	"fmt"
	"strings"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type CancelModule struct {
	sender    *telegram.MessageSender
	taskMgr   *ext.TaskManager
	userMap   *types.SafeUserMap
	statusMod *StatusModule
}

func NewCancelModule(sender *telegram.MessageSender, userMap *types.SafeUserMap, statusMod *StatusModule) *CancelModule {
	return &CancelModule{
		sender:    sender,
		taskMgr:   ext.GetTaskManager(),
		userMap:   userMap,
		statusMod: statusMod,
	}
}

func (m *CancelModule) HandleCancel(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	isCancel, extractedGID := telegram.MatchCancelCommand(msg.Text)
	if !isCancel && !msg.IsReply {
		return
	}

	if ext.IsPendingThumbSession(msg.UserID) {
		ext.ClearPendingThumbSession(msg.UserID)
		m.replyAndVanish(peer, msg, "✓ <b>Custom thumbnail upload cancelled.</b>")
		return
	}

	gid := extractedGID
	if gid == "" && msg.IsReply {
		for _, t := range m.taskMgr.GetAllTasks() {
			if t.MessageID() == msg.ReplyToID {
				gid = t.GID()
				break
			}
		}
		if gid == "" && msg.ReplyText != "" {
			for _, t := range m.taskMgr.GetAllTasks() {
				if strings.Contains(msg.ReplyText, t.GID()) {
					gid = t.GID()
					break
				}
			}
		}
	}

	if strings.ToLower(gid) == "all" {
		m.HandleCancelAll(ctx, peer, msg)
		return
	}

	if gid == "" {
		reply := "Reply to an active Command message which was used to start the download or send <code>/cancel_GID</code> to cancel it!"
		m.replyAndVanish(peer, msg, reply)
		return
	}

	task := m.taskMgr.GetTask(gid)
	if task == nil {
		m.replyAndVanish(peer, msg, fmt.Sprintf("GID: <code>%s</code> Not Found.", gid))
		return
	}

	// Permission: Owner, Sudo, or the user who started the task (same as Python AZML)
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) && task.UserID() != msg.UserID {
		m.replyAndVanish(peer, msg, "This task is not for you!")
		return
	}

	_ = task.Cancel()
	m.taskMgr.RemoveTask(gid)

	// Send confirmation and cleanup status asynchronously so HandleCancel returns immediately
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.replyAndVanish(peer, msg, fmt.Sprintf("✓ Task <code>%s</code> cancelled successfully!", gid))
		if m.statusMod != nil {
			if m.taskMgr.Count() == 0 {
				m.statusMod.DeleteAllMessages(bgCtx)
			} else {
				m.statusMod.UpdateAllMessages(bgCtx, true)
			}
		}
	}()
}

func (m *CancelModule) HandleCancelAll(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		m.replyAndVanish(peer, msg, "This command is only for Sudo/Owner!")
		return
	}

	tasks := m.taskMgr.GetAllTasks()
	if len(tasks) == 0 {
		m.replyAndVanish(peer, msg, "No active tasks!")
		return
	}

	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Downloading"), "canall downloading", "primary")
	btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Uploading"), "canall uploading", "primary")
	btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Paused"), "canall paused", "primary")
	btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Queued Dl"), "canall queueddl", "primary")
	btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Queued Up"), "canall queueup", "primary")
	btn.IButtonStyled("× "+telegram.ToSmallCaps("All Tasks"), "canall all", "danger")
	btn.IButtonStyled("❌", "canall close", "danger")

	_, _ = m.sender.SendMessageWithButtons(ctx, peer, "<b>Choose tasks to cancel:</b>", msg.MessageID, btn, 2)
}

func (m *CancelModule) HandleCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	if !telegram.IsOwner(fromUID) && !telegram.IsSudo(fromUID, m.userMap) {
		_ = m.sender.AnswerCallback(ctx, queryID, "Only Sudo/Owner can cancel tasks!", true)
		return
	}

	parts := strings.Fields(data)
	if len(parts) < 2 {
		return
	}

	action := strings.ToLower(parts[1])
	if action == "close" {
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed", false)
		m.sender.DeleteWithCommand(ctx, peer, msgID)
		return
	}

	count := m.taskMgr.CancelAll(action)
	_ = m.sender.EditMessage(ctx, peer, msgID, fmt.Sprintf("Cancelled <code>%d</code> task(s) matching '%s'", count, action), nil)
	_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Cancelled %d tasks", count), false)
	if m.statusMod != nil {
		if m.taskMgr.Count() == 0 {
			m.statusMod.DeleteAllMessages(ctx)
		} else {
			go m.statusMod.UpdateAllMessages(ctx, true)
		}
	}
}

// replyAndVanish answers the command in reply, then deletes the answer and the command after 3s.
func (m *CancelModule) replyAndVanish(peer tg.InputPeerClass, msg *types.MessageContext, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, _ := m.sender.SendMessage(ctx, peer, text, msg.MessageID, nil)
	go func() {
		time.Sleep(3 * time.Second)
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcancel()
		ids := []int{msg.MessageID}
		if id > 0 {
			ids = append([]int{id}, ids...)
		}
		_ = m.sender.DeleteMessage(dctx, peer, ids)
	}()
}
