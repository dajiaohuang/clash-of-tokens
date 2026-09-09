package majorweb

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gobwas/ws"
)

const poeSendHash = "f1486efc974a214dac6586c46b81bf631a95e58eab1d27b215f622859d74a23e"
const poeSubscribeHash = "5a7bfc9ce3b4e456cd05a537cfa27096f08417593b8d9b53f57587f3b7b63e99"
const poeBotHash = "2997adcc7abe30f763da42eed3174b67fd1b60ac4a23dac794526448c2629a8d"

func (c *Client) poeQuery(ctx context.Context, base string, h http.Header, name, hash string, vars any) (map[string]json.RawMessage, error) {
	payload, err := json.Marshal(struct {
		Query      string            `json:"queryName"`
		Variables  any               `json:"variables"`
		Extensions map[string]string `json:"extensions"`
	}{name, vars, map[string]string{"hash": hash}})
	if err != nil {
		return nil, errors.New("Poe invalid query payload")
	}
	signature := md5.Sum(append(append([]byte{}, payload...), []byte(h.Get("Poe-Formkey")+"4LxgHM6KpFqokX0Ox")...))
	h = h.Clone()
	h.Set("poe-tag-id", hex.EncodeToString(signature[:]))
	r, err := request(ctx, c.http, http.MethodPost, base+"/api/gql_POST", payload, h)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, &HTTPError{Status: r.StatusCode, What: "Poe GraphQL request failed"}
	}
	raw, err := readBounded(r.Body, 2<<20)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data    map[string]json.RawMessage `json:"data"`
		Errors  []json.RawMessage          `json:"errors"`
		Success *bool                      `json:"success"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Data == nil || len(result.Errors) > 0 || result.Success != nil && !*result.Success {
		return nil, errors.New("Poe GraphQL operation failed")
	}
	return result.Data, nil
}

type poeChannel struct {
	BaseHost string      `json:"baseHost"`
	Box      string      `json:"boxName"`
	Channel  string      `json:"channel"`
	Hash     string      `json:"channelHash"`
	Sequence json.Number `json:"minSeq"`
}

func poeChannelURL(base string, ch poeChannel) (string, error) {
	seq, err := ch.Sequence.Int64()
	if err != nil || seq < 0 || !huggingID.MatchString(ch.Box) || len(ch.Box) > 128 || ch.Channel == "" || len(ch.Channel) > 2048 || len(ch.Hash) > 2048 || ch.Hash == "" || strings.ContainsAny(ch.Channel, "\r\n") {
		return "", errors.New("Poe invalid channel settings")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", errors.New("Poe invalid origin")
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		if ch.BaseHost != "poe.com" {
			return "", errors.New("Poe unrecognized channel host")
		}
		u.Scheme = "wss"
		n, _ := strconv.ParseUint(randomID("")[:8], 16, 32)
		u.Host = "tch" + strconv.FormatUint(n%1000000+1, 10) + ".tch." + ch.BaseHost
	}
	u.Path = "/up/" + ch.Box + "/updates"
	u.RawQuery = url.Values{"min_seq": {strconv.FormatInt(seq, 10)}, "channel": {ch.Channel}, "hash": {ch.Hash}}.Encode()
	return u.String(), nil
}

func (c *Client) doPoe(parent context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 64<<10 || !huggingID.MatchString(model) || len(model) > 128 {
		return nil, &requestError{"Poe requires one text message and an exact bot handle"}
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") || cred.formkey == "" || len(cred.formkey) > 2048 || strings.ContainsAny(cred.formkey, "\r\n") {
		return nil, ErrCredential
	}
	cookies := map[string]bool{}
	for _, v := range (&http.Request{Header: http.Header{"Cookie": {cred.cookie}}}).Cookies() {
		if v.Value != "" {
			cookies[v.Name] = true
		}
	}
	if !cookies["p-b"] || !cookies["p-lat"] {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://poe.com")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	h := http.Header{"Content-Type": {"application/json"}, "Cookie": {cred.cookie}, "Poe-Formkey": {cred.formkey}, "Origin": {base}, "Referer": {base + "/"}}
	r, err := request(ctx, c.http, http.MethodGet, base+"/api/settings", nil, h)
	if err != nil {
		return nil, err
	}
	raw, readErr := readBounded(r.Body, 1<<20)
	r.Body.Close()
	if r.StatusCode != 200 {
		return nil, &HTTPError{Status: r.StatusCode, What: "Poe channel settings failed"}
	}
	if readErr != nil {
		return nil, readErr
	}
	var settings struct {
		Channel poeChannel `json:"tchannelData"`
	}
	if json.Unmarshal(raw, &settings) != nil {
		return nil, errors.New("Poe invalid channel response")
	}
	address, err := poeChannelURL(base, settings.Channel)
	if err != nil {
		return nil, err
	}
	h.Set("Poe-Tchannel", settings.Channel.Channel)
	subscriptions := []map[string]any{
		{"subscriptionName": "messageAdded", "query": nil, "queryHash": "993dcce616ce18788af3cce85e31437abf8fd64b14a3daaf3ae2f0e02d35aa03"},
		{"subscriptionName": "messageCancelled", "query": nil, "queryHash": "14647e90e5960ec81fa83ae53d270462c3743199fbb6c4f26f40f4c83116d2ff"},
	}
	if _, err = c.poeQuery(ctx, base, h, "SubscriptionsMutation", poeSubscribeHash, map[string]any{"subscriptions": subscriptions}); err != nil {
		return nil, err
	}
	dialer := ws.Dialer{Timeout: 15 * time.Second, Header: ws.HandshakeHeaderHTTP(http.Header{"Origin": {base}})}
	conn, prefetch, _, err := dialer.Dial(ctx, address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Poe channel connection failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	var reader io.Reader = conn
	if prefetch != nil {
		reader = io.MultiReader(prefetch, conn)
	}
	rw := &gatewayReadWriter{Reader: reader, Writer: conn}
	type received struct {
		raw []byte
		err error
	}
	queue := make(chan received, 16)
	readCtx, stopReader := context.WithCancel(ctx)
	defer stopReader()
	go func() {
		defer close(queue)
		total := 0
		for {
			raw, e := readBoundedServerText(rw, 1<<20)
			total += len(raw)
			if total > 16<<20 {
				e = errors.New("Poe channel response exceeds limit")
			}
			select {
			case queue <- received{raw, e}:
			case <-readCtx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	handle := strings.ToLower(model)
	botID := strings.ToLower(model)
	if id, ok := poeBots[model]; ok {
		botID = id
		handle = model
	} else {
		for name, id := range poeBots {
			if id == model {
				handle = name
				break
			}
		}
	}
	botData, err := c.poeQuery(ctx, base, h, "HandleBotLandingPageQuery", poeBotHash, map[string]string{"botHandle": handle})
	if err != nil {
		return nil, err
	}
	var bot struct {
		Model  string `json:"model"`
		Handle string `json:"handle"`
	}
	if json.Unmarshal(botData["bot"], &bot) != nil || bot.Handle == "" {
		return nil, errors.New("Poe bot unavailable")
	}
	// The pinned wrapper currently sends null for this price, because its bot
	// info projection comments out displayMessagePointPrice. Do not invent one.
	vars := map[string]any{"chatId": nil, "bot": botID, "query": in.Prompt, "shouldFetchChat": true, "source": map[string]any{"sourceType": "chat_input", "chatInputMetadata": map[string]bool{"useVoiceRecord": false}}, "clientNonce": randomID("")[:16], "sdid": "", "attachments": []any{}, "existingMessageAttachmentsIds": []any{}, "messagePointsDisplayPrice": nil}
	data, err := c.poeQuery(ctx, base, h, "SendMessageMutation", poeSendHash, vars)
	if err != nil {
		return nil, err
	}
	var sent struct {
		Status string `json:"status"`
		Chat   struct {
			ID json.Number `json:"chatId"`
		} `json:"chat"`
	}
	if json.Unmarshal(data["messageEdgeCreate"], &sent) != nil || sent.Status != "success" {
		return nil, errors.New("Poe message submission failed")
	}
	chatID, err := sent.Chat.ID.Int64()
	if err != nil || chatID <= 0 {
		return nil, errors.New("Poe missing new chat ID")
	}
	answer := ""
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case item, ok := <-queue:
			if !ok || item.err != nil {
				return nil, ErrTruncated
			}
			text, done, e := poeAnswerFrame(item.raw, sent.Chat.ID.String())
			if e != nil {
				return nil, e
			}
			if done {
				answer = text
				goto complete
			}
		}
	}
complete:
	if strings.TrimSpace(answer) == "" {
		return nil, errors.New("Poe empty completed answer")
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	var out []byte
	if stream {
		out = chatChunk(id, model, created, map[string]any{"role": "assistant", "content": answer}, nil)
		out = append(out, chatChunk(id, model, created, map[string]any{}, "stop")...)
		out = append(out, []byte("data: [DONE]\n\n")...)
	} else {
		out = chatCompletion(id, model, answer, "", created)
	}
	response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(out)), ContentLength: int64(len(out))}
	response.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		response.Header.Set("Content-Type", "text/event-stream")
	} else {
		response.Header.Set("Content-Type", "application/json")
	}
	return response, nil
}

func poeAnswerFrame(raw []byte, chatID string) (string, bool, error) {
	var outer struct {
		Error    json.RawMessage `json:"error"`
		Messages []string        `json:"messages"`
	}
	if json.Unmarshal(raw, &outer) != nil {
		return "", false, errors.New("Poe invalid channel frame")
	}
	if len(outer.Error) > 0 && string(outer.Error) != "null" {
		return "", false, errors.New("Poe channel error")
	}
	for _, value := range outer.Messages {
		var f struct {
			Type    string `json:"message_type"`
			Payload struct {
				Subscription string                     `json:"subscription_name"`
				ID           string                     `json:"unique_id"`
				Data         map[string]json.RawMessage `json:"data"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(value), &f) != nil {
			return "", false, errors.New("Poe invalid subscription frame")
		}
		if f.Type == "refetchChannel" {
			return "", false, errors.New("Poe channel requires reconnection")
		}
		p := f.Payload
		if p.ID != p.Subscription+":"+chatID {
			continue
		}
		if p.Subscription == "messageCancelled" {
			return "", false, errors.New("Poe message cancelled")
		}
		if p.Subscription != "messageAdded" {
			continue
		}
		var m struct {
			Author string `json:"author"`
			Text   string `json:"text"`
			State  string `json:"state"`
		}
		if json.Unmarshal(p.Data["messageAdded"], &m) != nil {
			return "", false, errors.New("Poe invalid message event")
		}
		if m.Author == "human" {
			continue
		}
		if strings.HasPrefix(m.State, "error") || m.State == "cancelled" {
			return "", false, errors.New("Poe response generation failed")
		}
		if m.State == "complete" {
			return m.Text, true, nil
		}
	}
	return "", false, nil
}

// Pinned frontend bot names and internal IDs from poe-api-wrapper utils.py.
var poeBots = map[string]string{
	"Assistant":               "capybara",
	"Claude-3.5-Sonnet":       "claude_3_igloo",
	"Claude-3-Opus":           "claude_2_1_cedar",
	"Claude-3-Sonnet":         "claude_2_1_bamboo",
	"Claude-3-Haiku":          "claude_3_haiku",
	"Claude-3-Opus-200k":      "claude_3_opus_200k",
	"Claude-3.5-Sonnet-200k":  "claude_3_igloo_200k",
	"Claude-3-Sonnet-200k":    "claude_3_sonnet_200k",
	"Claude-3-Haiku-200k":     "claude_3_haiku_200k",
	"Claude-2":                "claude_2_short",
	"Claude-2-100k":           "a2_2",
	"Claude-instant":          "a2",
	"Claude-instant-100k":     "a2_100k",
	"GPT-3.5-Turbo":           "chinchilla",
	"GPT-3.5-Turbo-Raw":       "gpt3_5",
	"GPT-3.5-Turbo-Instruct":  "chinchilla_instruct",
	"ChatGPT-16k":             "agouti",
	"GPT-4-Classic":           "gpt4_classic",
	"GPT-4-Turbo":             "beaver",
	"GPT-4-Turbo-128k":        "vizcacha",
	"GPT-4o":                  "gpt4_o",
	"GPT-4o-128k":             "gpt4_o_128k",
	"GPT-4o-Mini":             "gpt4_o_mini",
	"GPT-4o-Mini-128k":        "gpt4_o_mini_128k",
	"Google-PaLM":             "acouchy",
	"Code-Llama-13b":          "code_llama_13b_instruct",
	"Code-Llama-34b":          "code_llama_34b_instruct",
	"Solar-Mini":              "upstage_solar_0_70b_16bit",
	"Gemini-1.5-Flash-Search": "gemini_pro_search",
	"Gemini-1.5-Pro-2M":       "gemini_1_5_pro_1m",
}
