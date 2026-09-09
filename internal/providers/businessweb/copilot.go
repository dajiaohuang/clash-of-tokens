package businessweb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const defaultM365Host = "substrate.office.com"

var defaultM365Variants = []string{
	"EnableMcpServerWidgets", "feature.EnableMcpServerWidgets", "feature.EnableLuForChatCIQ",
	"feature.enableChatCIQPlugin", "EnableRequestPlugins", "feature.EnableSensitivityLabels",
	"EnableUnsupportedUrlDetector", "feature.IsCustomEngineCopilotEnabled", "feature.bizchatfluxv3",
	"feature.enablechatpages", "feature.enableCodeCanvas", "feature.turnOnDARecommendation",
	"feature.IsStreamingModeInChatRequestEnabled", "IncludeSourceAttributionsConcise",
	"SkipPublishEmptyMessage", "feature.EnableDeduplicatingSourceAttributions",
	"Enable3PActionProgressMessages", "feature.enableClientWebRtc", "feature.cwcfluxv3fe",
	"feature.cwcfluxv3fem", "feature.EnableReferencesListCompleteSignal", "feature.StorageMessageSplitDisabled",
	"feature.EnableCuaTakeControlApi", "SingletonEnvOn", "EnableComposeWidget", "feature.cwcallowedos",
	"feature.EnableMergingPureDeltas", "feature.disabledisallowedmsgs", "feature.enableCitationsForSynthesisData",
	"feature.EnableConversationShareApis", "feature.enableGenerateGraphicArtOptionsSet", "cdximagen",
	"feature.EnableUpdatedUXForConfirmationDialog", "feature.EnableContentApiandDocTypeHtmlInRichAnswers",
	"cdxgrounding_api_v2_rich_web_answers_reference_bottom_force", "cdxenablerenderforisocomp",
	"feature.EnableClientFileURLSupportForOfficeWebPaidCopilot", "feature.EnableDesignEditorImageGrounding",
	"feature.EnableDesignerEditor", "feature.EnableSkipRehydrationForSpeCIdImages", "feature.EnablePersonalizationForMSA",
	"agt_bizchat_enableRichResponses", "feature.EnableBase64DataInMessageAnnotations",
	"feature.EnableSkipEmittingMessageOnFlush", "feature.EnableRemoveEmptySourceAttributions",
	"feature.EnableStreamingMode", "feature.EnableRemoveStreamingMode",
}

func (c *Client) doCopilotM365(ctx context.Context, req chatRequest) (*http.Response, error) {
	if strings.TrimSpace(req.Model) != "web" {
		return nil, &unsupportedError{"business web adapter: M365 supports only the web model"}
	}
	cred, err := credential(c.source)
	if err != nil {
		return nil, err
	}
	wsURL, err := m365URL(c.source.BaseURL, cred)
	if err != nil {
		return nil, err
	}
	conn, prefetch, _, err := (&ws.Dialer{Timeout: 20 * time.Second, Header: ws.HandshakeHeaderHTTP(http.Header{"Origin": {"https://m365.cloud.microsoft"}, "User-Agent": {"Mozilla/5.0"}})}).Dial(ctx, wsURL)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("business web adapter: M365 WebSocket connection failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	// The WebSocket upgrade timeout ends once the socket is established. Bound
	// the SignalR handshake separately so an accepted but silent peer cannot
	// hold a request indefinitely.
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return nil, errors.New("business web adapter: M365 WebSocket deadline failed")
	}
	var reader io.Reader = conn
	if prefetch != nil {
		reader = io.MultiReader(prefetch, conn)
	}
	rw := &copilotReadWriter{Reader: reader, Writer: conn}
	if err := writeWS(conn, map[string]any{"protocol": "json", "version": 1}); err != nil {
		return nil, err
	}
	handshake, err := readWS(rw, maxEventBytes)
	if err != nil {
		return nil, ErrTruncated
	}
	handshake = []byte(strings.TrimSpace(strings.TrimSuffix(string(handshake), "\x1e")))
	var hs map[string]any
	if json.Unmarshal([]byte(handshake), &hs) != nil {
		return nil, errors.New("business web adapter: invalid M365 handshake")
	}
	if msg, _ := hs["error"].(string); msg != "" {
		return nil, errors.New("business web adapter: M365 handshake rejected")
	}
	turnDeadline := time.Now().Add(120 * time.Second)
	if err := conn.SetDeadline(turnDeadline); err != nil {
		return nil, errors.New("business web adapter: M365 WebSocket deadline failed")
	}
	responseBytes := len(handshake)
	chat := m365Invocation(req, wsURL)
	b, _ := json.Marshal(chat)
	metrics := map[string]any{"arguments": []any{map[string]any{"Timestamps": map[string]string{"ConnectionEstablished": "", "ConnectionStart": "", "UserInputStart": "", "UserInputSubmit": ""}}}, "target": "Metrics", "type": 1}
	mb, _ := json.Marshal(metrics)
	combined := append(append(b, 0x1e), append(mb, 0x1e)...)
	if err := wsutil.WriteClientText(conn, combined); err != nil {
		return nil, errors.New("business web adapter: M365 invocation failed")
	}
	var answer, previous string
	done := false
	var pending []string
	for !done {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if len(pending) == 0 {
			raw, err := readWS(rw, maxEventBytes)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if ne, ok := err.(net.Error); ok && ne.Timeout() && !time.Now().Before(turnDeadline) {
					return nil, errors.New("business web adapter: M365 WebSocket timed out")
				}
				return nil, ErrTruncated
			}
			responseBytes += len(raw)
			if responseBytes > maxResponseBytes {
				return nil, errors.New("business web adapter: M365 response exceeds limit")
			}
			for _, part := range strings.Split(string(raw), "\x1e") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					pending = append(pending, trimmed)
				}
			}
		}
		if len(pending) == 0 {
			continue
		}
		raw := []byte(pending[0])
		pending = pending[1:]
		var frame map[string]any
		if json.Unmarshal(raw, &frame) != nil {
			return nil, errors.New("business web adapter: invalid M365 SignalR frame")
		}
		if !m365FrameMatchesInvocation(frame, "0") {
			continue
		}
		if typ, _ := frame["type"].(float64); typ == 6 {
			if err := writeWS(conn, map[string]any{"type": 6}); err != nil {
				return nil, errors.New("business web adapter: M365 keepalive failed")
			}
			continue
		}
		if e := m365FrameError(frame); e != "" {
			return nil, errors.New("business web adapter: M365 invocation failed")
		}
		if delta := m365FrameWriteAtCursor(frame); delta != "" {
			answer += delta
			previous = answer
		} else if text := m365FrameText(frame); text != "" {
			if strings.HasPrefix(text, previous) {
				answer += text[len(previous):]
			} else if text != previous {
				answer = text
			}
			previous = text
		}
		if len(answer) > maxResponseBytes {
			return nil, errors.New("business web adapter: M365 response exceeds limit")
		}
		if typ, _ := frame["type"].(float64); typ == 3 {
			done = true
		}
	}
	if strings.TrimSpace(answer) == "" {
		return nil, errors.New("business web adapter: M365 completed without answer")
	}
	if req.Stream {
		return streamResponse(req.Model, answer, ""), nil
	}
	return jsonResponse(req.Model, answer, ""), nil
}

type copilotReadWriter struct {
	io.Reader
	io.Writer
}

func writeWS(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, 0x1e)
	return wsutil.WriteClientText(w, b)
}

// m365FrameMatchesInvocation filters SignalR frames that belong to this
// request. Update frames commonly omit invocationId, while completion frames
// must carry the id of the invocation they complete.
func m365FrameMatchesInvocation(frame map[string]any, expected string) bool {
	typ, _ := frame["type"].(float64)
	if typ != 1 && typ != 2 && typ != 3 {
		return true
	}
	id, present := frame["invocationId"]
	if !present {
		return typ != 3
	}
	value, ok := id.(string)
	return ok && value == expected
}

func readWS(rw io.ReadWriter, limit int64) ([]byte, error) {
	reader := wsutil.NewReader(rw, ws.StateClientSide)
	reader.CheckUTF8 = true
	reader.MaxFrameSize = limit
	reader.OnIntermediate = wsutil.ControlFrameHandler(rw, ws.StateClientSide)
	for {
		h, err := reader.NextFrame()
		if err != nil {
			return nil, err
		}
		if h.OpCode.IsControl() {
			if !reader.State.Fragmented() {
				if err := wsutil.ControlFrameHandler(rw, ws.StateClientSide)(h, reader); err != nil {
					return nil, err
				}
			}
			continue
		}
		if h.OpCode != ws.OpText {
			if err := reader.Discard(); err != nil {
				return nil, err
			}
			continue
		}
		b, err := io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit {
			return nil, errors.New("WebSocket event exceeds limit")
		}
		return b, nil
	}
}

func m365URL(raw string, cred map[string]string) (string, error) {
	token := strings.TrimSpace(credOr(cred, "access_token", ""))
	if token == "" {
		token = strings.TrimSpace(credOr(cred, "accessToken", ""))
	}
	if token == "" {
		token = stripBearer(cred["value"])
	}
	if token == "" || strings.ContainsAny(token, "\r\n\x00") {
		return "", ErrCredential
	}
	path := strings.TrimSpace(credOr(cred, "chathub_path", ""))
	if path == "" {
		path = credOr(cred, "chathubPath", "")
	}
	if path == "" {
		path = credOr(cred, "userTenant", "")
	}
	if path == "" {
		return "", errors.New("business web adapter: M365 credential requires user-tenant Chathub path")
	}
	if !strings.Contains(path, "@") || strings.ContainsAny(path, "\r\n?#") {
		return "", ErrCredential
	}
	base := strings.TrimSpace(raw)
	if base == "" {
		base = "wss://" + defaultM365Host + "/m365Copilot/Chathub/" + path
	} else if !strings.Contains(base, "://") {
		base = "wss://" + base
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil {
		return "", errors.New("business web adapter: invalid M365 WebSocket URL")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	if u.Scheme != "wss" && !(u.Scheme == "ws" && isLoopback(u.Hostname())) {
		return "", errors.New("business web adapter: M365 WebSocket must use WSS")
	}
	if !strings.Contains(u.Path, "/Chathub/") {
		u.Path = "/m365Copilot/Chathub/" + url.PathEscape(path)
	}
	q := u.Query()
	if q.Get("access_token") == "" {
		q.Set("access_token", token)
	}
	if q.Get("chatsessionid") == "" {
		id := randomID("")
		q.Set("chatsessionid", id)
		q.Set("XRoutingParameterSessionKey", id)
		q.Set("clientrequestid", id)
		q.Set("X-SessionId", randomID(""))
		q.Set("ConversationId", randomID(""))
	}
	if q.Get("XRoutingParameterSessionKey") == "" {
		q.Set("XRoutingParameterSessionKey", q.Get("chatsessionid"))
	}
	if q.Get("variants") == "" {
		q.Set("variants", credOr(cred, "variants", strings.Join(defaultM365Variants, ",")))
	}
	if q.Get("source") == "" {
		q.Set("source", credOr(cred, "source", "officeweb"))
	}
	if q.Get("product") == "" {
		q.Set("product", credOr(cred, "product", "Office"))
	}
	if q.Get("agentHost") == "" {
		q.Set("agentHost", credOr(cred, "agent_host", "Bizchat.FullScreen"))
	}
	if q.Get("licenseType") == "" {
		q.Set("licenseType", credOr(cred, "license_type", "Starter"))
	}
	if q.Get("isEdu") == "" {
		q.Set("isEdu", credOr(cred, "is_edu", "false"))
	}
	if q.Get("agent") == "" {
		q.Set("agent", credOr(cred, "agent", "web"))
	}
	if q.Get("scenario") == "" {
		q.Set("scenario", credOr(cred, "scenario", "OfficeWebPaidConsumerCopilot"))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func m365Invocation(req chatRequest, wsURL string) map[string]any {
	u, _ := url.Parse(wsURL)
	requestID := u.Query().Get("chatsessionid")
	sessionID := u.Query().Get("X-SessionId")
	conversationID := u.Query().Get("ConversationId")
	text := flattenPrompt(req.Messages)
	clientInfo := map[string]any{"clientAppName": "Office", "clientPlatform": "mcmcopilot-web", "clientEntrypoint": "mcmcopilot-officeweb", "clientSessionId": sessionID, "ProductCategory": "Chat", "clientAppType": "Web", "productEntryPoint": "ChatPanel", "deviceOS": "Windows", "deviceType": "Desktop", "clientPlatformVersion": "10"}
	args := map[string]any{"allowedMessageTypes": []string{"Chat", "Suggestion", "Progress", "GeneratedCode", "EndOfRequest", "ReferencesListComplete"}, "clientCorrelationId": requestID, "clientInfo": clientInfo, "conversationId": conversationID, "extraExtensionParameters": map[string]any{}, "isSbsSupported": true, "isStartOfSession": true, "message": map[string]any{"adaptiveCards": []any{}, "attachments": nil, "author": "user", "clientInfo": clientInfo, "clientPreferences": map[string]any{}, "connectedFederatedConnections": []string{"dummyId"}, "entityAnnotationTypes": []string{"People", "File", "Event", "Email", "TeamsMessage"}, "experienceType": "Default", "inputMethod": "Keyboard", "locale": "en-us", "locationInfo": map[string]any{"timeZone": "UTC", "timeZoneOffset": 0}, "messageType": "Chat", "requestId": requestID, "text": text}, "options": map[string]any{}, "optionsSets": []string{"rich_responses", "cwc_flux_v3", "enable_batch_token_processing", "feature.EnableMcpServerWidgets", "feature.EnableReferencesListCompleteSignal", "feature.EnableMergingPureDeltas"}, "plugins": []any{map[string]any{"Id": "BingWebSearch", "Source": "BuiltIn"}}, "productThreadType": "Office", "renderReferencesBehindEOS": true, "sessionId": sessionID, "sliceIds": []string{}, "source": "officeweb", "streamingMode": "ConciseWithPadding", "threadLevelGptId": map[string]any{}, "tone": "Magic", "toolChoice": nil, "traceId": randomID(""), "disconnectBehavior": "continue"}
	return map[string]any{"type": 4, "target": "chat", "invocationId": "0", "arguments": []any{args}}
}
func flattenPrompt(messages []chatMessage) string {
	var b strings.Builder
	for _, m := range messages {
		s, _ := textContent(m.Content)
		if s == "" {
			continue
		}
		if m.Role == "system" {
			b.WriteString("System: ")
			b.WriteString(s)
		} else if m.Role == "assistant" {
			b.WriteString("Assistant: ")
			b.WriteString(s)
		} else {
			b.WriteString(s)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
func m365FrameText(frame map[string]any) string {
	typ, _ := frame["type"].(float64)
	if typ != 1 && typ != 2 && typ != 3 {
		return ""
	}
	if args, ok := frame["arguments"].([]any); ok {
		if len(args) > 0 {
			if first, ok := args[0].(map[string]any); ok {
				// An update carrying messages is a source snapshot. If all of its
				// messages are progress/tool content, do not fall through to a
				// generic text walk that could accept user or tool text as an answer.
				if _, hasMessages := first["messages"]; hasMessages {
					return m365MessagesText(first)
				}
			}
		}
	}
	if typ == 2 {
		if item, ok := frame["item"].(map[string]any); ok {
			if result, ok := item["result"].(map[string]any); ok {
				if text, _ := result["message"].(string); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func m365FrameWriteAtCursor(frame map[string]any) string {
	if typ, _ := frame["type"].(float64); typ != 1 {
		return ""
	}
	args, ok := frame["arguments"].([]any)
	if !ok || len(args) == 0 {
		return ""
	}
	first, ok := args[0].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := first["writeAtCursor"].(string)
	return text
}

func m365MessagesText(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	messages, ok := m["messages"].([]any)
	if !ok {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := message["messageType"].(string); typ == "Progress" {
			continue
		}
		if typ, _ := message["contentType"].(string); typ == "SearchResults" || typ == "Code" || typ == "ToolCall" || typ == "EarlyProgress" {
			continue
		}
		author, _ := message["author"].(string)
		if author != "" && author != "bot" {
			continue
		}
		if text, _ := message["text"].(string); text != "" {
			return text
		}
	}
	return ""
}
func m365FrameError(frame map[string]any) string {
	if e, ok := frame["error"].(string); ok {
		return e
	}
	if e, ok := frame["error"].(map[string]any); ok {
		if s, _ := e["message"].(string); s != "" {
			return s
		}
		return "error"
	}
	return ""
}
