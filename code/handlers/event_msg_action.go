package handlers

import (
	"encoding/json"
	"fmt"
	"github.com/k0kubun/pp/v3"
	"log"
	"start-feishubot/initialization"
	"start-feishubot/services/accesscontrol"
	"start-feishubot/services/chatgpt"
	"start-feishubot/services/openai"
	"strings"
	"time"
)

type MessageAction struct { /*消息*/
	chatgpt *chatgpt.ChatGPT
}

func (m *MessageAction) Execute(a *ActionInfo) bool {

	// Add access control
	if initialization.GetConfig().AccessControlEnable &&
		!accesscontrol.CheckAllowAccessThenIncrement(&a.info.userId) {

		msg := fmt.Sprintf("UserId: 【%s】 has accessed max count today! Max access count today %s: 【%d】",
			a.info.userId, accesscontrol.GetCurrentDateFlag(), initialization.GetConfig().AccessControlMaxCountPerUserPerDay)

		_ = sendMsg(*a.ctx, msg, a.info.chatId)
		return false
	}

	//s := "快速响应，用于测试： " + time.Now().String() +
	//	" accesscontrol.currentDate " + accesscontrol.GetCurrentDateFlag()
	//_ = sendMsg(*a.ctx, s, a.info.chatId)
	//log.Println(s)
	//return false

	//if new topic
	var ifNewTopic bool
	if len(msg) <= 3 {
		ifNewTopic = true
	} else {
		ifNewTopic = false
	}
	
	cardId, err2 := sendOnProcess(a, ifNewTopic)
	if err2 != nil {
		return false
	}

	answer := ""
	chatResponseStream := make(chan string)
	done := make(chan struct{}) // 添加 done 信号，保证 goroutine 正确退出
	noContentTimeout := time.AfterFunc(10*time.Second, func() {
		pp.Println("no content timeout")
		close(done)
		err := updateFinalCard(*a.ctx, "请求超时", cardId, ifNewTopic)
		if err != nil {
			return
		}
		return
	})
	defer noContentTimeout.Stop()
	msg := a.handler.sessionCache.GetMsg(*a.info.sessionId)
	// 如果没有提示词，默认模拟ChatGPT
	if !hasSystemRole(msg) {
		msg = append(msg, openai.Messages{
			Role: "system", Content: "You are ChatGPT, " +
				"a large language model trained by OpenAI. " +
				"Answer in user's language as concisely as" +
				" possible. Knowledge cutoff: 20230601 " +
				"Current date" + time.Now().Format("20060102"),
		})
	}
	msg = append(msg, openai.Messages{
		Role: "user", Content: a.info.qParsed,
	})
	go func() {
		defer func() {
			if err := recover(); err != nil {
				err := updateFinalCard(*a.ctx, "聊天失败", cardId, ifNewTopic)
				if err != nil {
					printErrorMessage(a, msg, err)
					return
				}
			}
		}()

		//log.Printf("UserId: %s , Request: %s", a.info.userId, msg)

		if err := m.chatgpt.StreamChat(*a.ctx, msg, chatResponseStream); err != nil {
			err := updateFinalCard(*a.ctx, "聊天失败", cardId, ifNewTopic)
			if err != nil {
				printErrorMessage(a, msg, err)
				return
			}
			close(done) // 关闭 done 信号
		}

		close(done) // 关闭 done 信号
	}()
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop() // 注意在函数结束时停止 ticker
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				err := updateTextCard(*a.ctx, answer, cardId)
				if err != nil {
					printErrorMessage(a, msg, err)
					return
				}
			}
		}
	}()

	for {
		select {
		case res, ok := <-chatResponseStream:
			if !ok {
				return false
			}
			noContentTimeout.Stop()
			answer += res
			//pp.Println("answer", answer)
		case <-done: // 添加 done 信号的处理
			err := updateFinalCard(*a.ctx, answer, cardId, ifNewTopic)
			if err != nil {
				printErrorMessage(a, msg, err)
				return false
			}
			ticker.Stop()
			msg := append(msg, openai.Messages{
				Role: "assistant", Content: answer,
			})
			a.handler.sessionCache.SetMsg(*a.info.sessionId, msg)
			close(chatResponseStream)
			//if new topic
			//if len(msg) == 2 {
			//	//fmt.Println("new topic", msg[1].Content)
			//	//updateNewTextCard(*a.ctx, a.info.sessionId, a.info.msgId,
			//	//	completions.Content)
			//}
			log.Printf("\n\n\n")
			log.Printf("Success request: UserId: %s , Request: %s , Response: %s", a.info.userId, msg, answer)
			jsonByteArray, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Error marshaling JSON request: UserId: %s , Request: %s , Response: %s", a.info.userId, jsonByteArray, answer)
			}
			jsonStr := strings.ReplaceAll(string(jsonByteArray), "\\n", "")
			jsonStr = strings.ReplaceAll(jsonStr, "\n", "")
			log.Printf("\n\n\n")
			log.Printf("Success request plain jsonStr: UserId: %s , Request: %s , Response: %s",
				a.info.userId, jsonStr, answer)
			return false
		}
	}
}

func printErrorMessage(a *ActionInfo, msg []openai.Messages, err error) {
	log.Printf("Failed request: UserId: %s , Request: %s , Err: %s", a.info.userId, msg, err)
}

func sendOnProcess(a *ActionInfo, ifNewTopic bool) (*string, error) {
	// send 正在处理中
	cardId, err := sendOnProcessCard(*a.ctx, a.info.sessionId, a.info.msgId, ifNewTopic)
	if err != nil {
		return nil, err
	}
	return cardId, nil

}

//判断msg中的是否包含system role
func hasSystemRole(msg []openai.Messages) bool {
	for _, m := range msg {
		if m.Role == "system" {
			return true
		}
	}
	return false
}
