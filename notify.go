/*
Package notify 对企业微信应用消息发送接口进行了封装.

接口文档见：https://work.weixin.qq.com/api/doc/90001/90143/90372
*/
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

const (
	apiPrefix = "https://qyapi.weixin.qq.com/cgi-bin"
)

type UploadMedia struct {
	Type string
	Path string
}

type UploadMediaResult struct {
	ErrorCode int64  `json:"errcode"` // 错误码，0为全部成功
	ErrorMsg  string `json:"errmsg"`
	Type      string `json:"type"`
	MediaID   string `json:"media_id"`
	CreatedAt string `json:"created_at"`
}

// MessageReceiver 消息接收者 ToUser、ToParty、ToTag 至少一个
type MessageReceiver struct {
	ToUser  string `json:"touser"`  // 成员ID列表（消息接收者，多个接收者用‘|’分隔，最多支持1000个）。特殊情况：指定为@all，则向关注该企业应用的全部成员发送
	ToParty string `json:"toparty"` // 指定接收消息的部门，部门ID列表，多个接收者用‘|’分隔，最多支持100个。当touser为”@all”时忽略本参数
	ToTag   string `json:"totag"`   // 指定接收消息的标签，标签ID列表，多个接收者用‘|’分隔，最多支持100个。 当touser为”@all”时忽略本参数
}

// MessageOptions 消息配置包括加密、id转译、重复检查等。部分消息类型只支持部分配置详见官方文档
type MessageOptions struct {
	Safe                   bool `json:"safe"`                     // 表示是否是保密消息，默认否
	EnableIDTrans          bool `json:"enable_id_trans"`          // 表示是否开启id转译，默认否
	EnableDuplicateCheck   bool `json:"enable_duplicate_check"`   // 表示是否开启重复消息检查，默认否
	DuplicateCheckInterval int  `json:"duplicate_check_interval"` // 表示是否重复消息检查的时间间隔，默认1800s，最大不超过4小时
}

// MessageResult 消息发送结果。如果部分接收人无权限或不存在，发送仍然执行，但会返回无效的部分（即invaliduser或invalidparty或invalidtag），常见的原因是接收人不在应用的可见范围内。
// 如果全部接收人无权限或不存在，则本次调用返回失败，errcode为81013。
// 返回包中的userid，不区分大小写，统一转为小写
type MessageResult struct {
	ErrorCode    int64  `json:"errcode"` // 错误码，0为全部成功
	ErrorMsg     string `json:"errmsg"`
	InvalidUser  string `json:"invaliduser"`
	InvalidParty string `json:"invalidparty"`
	InvalidTag   string `json:"invalidtag"`
}

type MessageKey interface {
	key() string
}

// Text 文本消息
type Text struct {
	Content string `json:"content"` // 消息内容，最长不超过2048个字节，超过将截断（支持id转译）
}

func (t Text) key() string {
	return "text"
}

// Image 图片消息
type Image struct {
	MediaID string `json:"media_id"` // 图片媒体文件id，可以调用上传临时素材接口获取
}

func (t Image) key() string {
	return "image"
}

// Voice 语音消息
type Voice struct {
	MediaID string `json:"media_id"` // 语音文件id，可以调用上传临时素材接口获取
}

func (t Voice) key() string {
	return "voice"
}

// Video 视频消息
type Video struct {
	MediaID     string `json:"media_id"`              // 视频媒体文件id，可以调用上传临时素材接口获取
	Title       string `json:"title,omitempty"`       // 非必填。视频消息的标题，不超过128个字节，超过会自动截断
	Description string `json:"description,omitempty"` // 非必填。视频消息的描述，不超过512个字节，超过会自动截断
}

func (t Video) key() string {
	return "video"
}

// File 文件消息
type File struct {
	MediaID string `json:"media_id"` // 文件id，可以调用上传临时素材接口获取
}

func (t File) key() string {
	return "file"
}

// TextCard 文本卡片消息
type TextCard struct {
	Title       string `json:"title"`            // 标题，不超过128个字节，超过会自动截断（支持id转译）
	Description string `json:"description"`      // 描述，不超过512个字节，超过会自动截断（支持id转译）
	URL         string `json:"url"`              // 点击后跳转的链接
	BtnTxt      string `json:"btntxt,omitempty"` // 非必填。按钮文字。 默认为“详情”， 不超过4个文字，超过自动截断
}

func (t TextCard) key() string {
	return "textcard"
}

// News 图文消息
type News struct {
	Articles []NewsArticle `json:"articles"` // 图文消息，一个图文消息支持1到8条图文
}

func (t News) key() string {
	return "news"
}

// NewsArticle 图文消息详情
type NewsArticle struct {
	Title       string `json:"title"`                 // 标题，不超过128个字节，超过会自动截断（支持id转译）
	Description string `json:"description,omitempty"` // 非必填。描述，不超过512个字节，超过会自动截断（支持id转译）
	URL         string `json:"url"`                   // 点击后跳转的链接。
	PicURL      string `json:"picurl,omitempty"`      // 非必填。图文消息的图片链接，支持JPG、PNG格式，较好的效果为大图 1068*455，小图150*150
}

// MpNews 图文消息（mpnews）。mpnews类型的图文消息，跟普通的图文消息一致，唯一的差异是图文内容存储在企业微信。
// 多次发送mpnews，会被认为是不同的图文，阅读、点赞的统计会被分开计算。
type MpNews struct {
	Articles []MpNewsArticle `json:"articles"` // 图文消息，一个图文消息支持1到8条图文
}

func (t MpNews) key() string {
	return "mpnews"
}

// MpNewsArticle 图文消息详情详情
type MpNewsArticle struct {
	Title            string `json:"title"`                        // 标题，不超过128个字节，超过会自动截断（支持id转译）
	ThumbMediaID     string `json:"thumb_media_id"`               // 图文消息缩略图的media_id, 可以通过素材管理接口获得。此处thumb_media_id即上传接口返回的media_id
	Author           string `json:"author,omitempty"`             // 非必填。图文消息的作者，不超过64个字节
	ContentSourceURL string `json:"content_source_url,omitempty"` // 非必填。图文消息点击“阅读原文”之后的页面链接
	Content          string `json:"content"`                      // 图文消息的内容，支持html标签，不超过666 K个字节（支持id转译）
	Digest           string `json:"digest,omitempty"`             // 非必填。图文消息的描述，不超过512个字节，超过会自动截断（支持id转译）
}

// Markdown Markdown消息
type Markdown struct {
	Content string `json:"content"` // markdown内容，最长不超过2048个字节，必须是utf8编码
}

func (t Markdown) key() string {
	return "markdown"
}

// MiniProgram 小程序通知消息。小程序通知消息只允许小程序应用发送，
// 从2019年6月28日起，用户收到的小程序通知会出现在各个独立的小程序应用中。
// 小程序应用仅支持发送小程序通知消息，暂不支持文本、图片、语音、视频、图文等其他类型的消息。
// 不支持@all全员发送
type MiniProgram struct {
	AppID             string                   `json:"appid"`                         // 小程序appid，必须是与当前小程序应用关联的小程序
	Page              string                   `json:"page,omitempty"`                // 非必填。点击消息卡片后的小程序页面，仅限本小程序内的页面。该字段不填则消息点击后不跳转。
	Title             string                   `json:"title"`                         // 消息标题，长度限制4-12个汉字（支持id转译）
	Description       string                   `json:"description,omitempty"`         // 非必填。消息描述，长度限制4-12个汉字（支持id转译）
	EmphasisFirstItem bool                     `json:"emphasis_first_item,omitempty"` // 非必填。是否放大第一个 content_item
	ContentItems      []MiniProgramContentItem `json:"content_item,omitempty"`        // 非必填。消息内容键值对，最多允许10个item
}

func (t MiniProgram) key() string {
	return "miniprogram_notice"
}

// MiniProgramContentItem 小程序通知消息内容键值
type MiniProgramContentItem struct {
	Key   string `json:"key"`   // 长度10个汉字以内
	Value string `json:"value"` // 长度30个汉字以内（支持id转译）
}

// TaskCard 任务卡片消息。仅企业微信2.8.2及以上版本支持
type TaskCard struct {
	Title       string           `json:"title"`         // 标题，不超过128个字节，超过会自动截断（支持id转译）
	Description string           `json:"description"`   // 描述，不超过512个字节，超过会自动截断（支持id转译）
	URL         string           `json:"url,omitempty"` // 非必填。点击后跳转的链接。最长2048字节，请确保包含了协议头(http/https)
	TaskID      string           `json:"task_id"`       // 任务id，同一个应用发送的任务卡片消息的任务id不能重复，只能由数字、字母和“_-@.”组成，最长支持128字节
	Buttons     []TaskCardButton `json:"btn"`           // 按钮列表，按钮个数为为1~2个
}

func (t TaskCard) key() string {
	return "taskcard"
}

// TaskCardButton  任务卡片消息操作按钮
type TaskCardButton struct {
	Key         string `json:"key"`          // 按钮key值，用户点击后，会产生任务卡片回调事件，回调事件会带上该key值，只能由数字、字母和“_-@.”组成，最长支持128字节
	Name        string `json:"name"`         // 按钮名称
	ReplaceName string `json:"replace_name"` // 非必填。 点击按钮后显示的名称，默认为“已处理”
	Color       string `json:"color"`        // 非必填。 按钮字体颜色，可选“red”或者“blue”,默认为“blue”
	IsBold      bool   `json:"is_bold"`      // 非必填。按钮字体是否加粗，默认false
}

// Notify reference to call send method
type Notify struct {
	corpID    string
	agentID   int64
	appSecret string

	TokenPersist   bool
	Token          string
	TokenExpiresAt int64
	CacheFilePath  string // 新增缓存文件路径配置
}

type GetTokenResult struct {
	ErrorCode int    `json:"errcode,omitempty"`
	ErrorMsg  string `json:"errmsg,omitempty"`
	Token     string `json:"access_token,omitempty"`
	ExpiresIn int64  `json:"expires_in,omitempty"`
}

// New client，corpID 企业ID，在企业信息页面查看, agentID + appSecret 在应用页面查看
func New(corpID string, agentID int64, appSecret string) *Notify {
	n := &Notify{
		corpID: corpID, agentID: agentID, appSecret: appSecret,
		CacheFilePath: ".notify", // 默认缓存文件路径
	}
	_ = n.loadTokenCache()
	return n
}

// Send message with options to receiver, options can be nil
func (n *Notify) Send(receiver MessageReceiver, message interface{}, options *MessageOptions) (MessageResult, error) {
	var result MessageResult
	if message == nil {
		return result, errors.New("message can not be nil")
	}

	msgBody := make(map[string]interface{})

	if len(receiver.ToUser) == 0 && len(receiver.ToParty) == 0 && len(receiver.ToTag) == 0 {
		return result, errors.New("message receiver not set, set at least one")
	}

	msgBody["touser"] = receiver.ToUser
	msgBody["toparty"] = receiver.ToParty
	msgBody["totag"] = receiver.ToTag
	msgBody["agentid"] = n.agentID
	setOptions(msgBody, options)

	k, ok := message.(MessageKey)
	if !ok {
		return result, fmt.Errorf("unrecognized message type: %T", reflect.TypeOf(message))
	}
	msgBody["msgtype"] = k.key()
	msgBody[k.key()] = message

	return n.sendInternal(msgBody)
}

// setOptions for message
func setOptions(msgBody map[string]interface{}, options *MessageOptions) {
	if options != nil {
		if options.Safe {
			msgBody["safe"] = 1
		}
		if options.EnableIDTrans {
			msgBody["enable_id_trans"] = 1
		}
		if options.EnableDuplicateCheck {
			msgBody["enable_duplicate_check"] = 1
			if options.DuplicateCheckInterval != 0 {
				msgBody["duplicate_check_interval"] = options.DuplicateCheckInterval
			}
		}
	}
}

// Upload temp media to server
func (n *Notify) Upload(media UploadMedia) (UploadMediaResult, error) {
	var result UploadMediaResult
	var client = &http.Client{Timeout: 10 * time.Second}

	// read media file
	f, err := os.Open(media.Path)
	if err != nil {
		return result, fmt.Errorf("open media file error: %w", err)
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("media", filepath.Base(media.Path))
	if err != nil {
		return result, fmt.Errorf("create multipart file error: %w", err)
	}
	_, err = io.Copy(fw, f)
	if err != nil {
		return result, fmt.Errorf("read media file error: %w", err)
	}
	_ = w.Close()
	// get token
	token, _, err := n.GetToken()
	if err != nil {
		return result, err
	}
	fmt.Println(token)
	// send request
	res, err := client.Post(fmt.Sprintf("%s/media/upload?access_token=%s&type=%s", apiPrefix, n.Token, media.Type), w.FormDataContentType(), &b)
	if err != nil {
		return result, fmt.Errorf("upload media file error: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	err = json.NewDecoder(res.Body).Decode(&result)
	if err != nil {
		return result, fmt.Errorf("upload media result decode error: %w", err)
	}
	return result, nil
}

func (n *Notify) EnableTokenPersist() {
	n.TokenPersist = true
}

// SetCacheFilePath 设置缓存文件路径
func (n *Notify) SetCacheFilePath(path string) {
	n.CacheFilePath = path
}

func (n *Notify) GetToken() (string, int64, error) {
	if n.Token != "" && time.Now().Unix() < n.TokenExpiresAt {
		return n.Token, n.TokenExpiresAt, nil
	}

	var client = &http.Client{Timeout: 10 * time.Second}

	res, err := client.Get(fmt.Sprintf("%s/gettoken?corpid=%s&corpsecret=%s", apiPrefix, n.corpID, n.appSecret))
	if err != nil {
		return "", 0, fmt.Errorf("token get request error: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	var tokenRes GetTokenResult
	err = json.NewDecoder(res.Body).Decode(&tokenRes)
	if err != nil {
		return "", 0, fmt.Errorf("token result decode error: %w", err)
	}
	if tokenRes.ErrorCode != 0 {
		return "", 0, fmt.Errorf("token get error: %s", tokenRes.ErrorMsg)
	}
	n.Token = tokenRes.Token
	n.TokenExpiresAt = time.Now().Unix() + tokenRes.ExpiresIn

	_ = n.saveTokenCache()

	return tokenRes.Token, n.TokenExpiresAt, nil
}

func (n *Notify) loadTokenCache() error {
	if !n.TokenPersist {
		return fmt.Errorf("token persist not enabled")
	}

	// 使用配置的缓存文件路径
	b, err := os.ReadFile(n.CacheFilePath)
	if err != nil {
		return fmt.Errorf("read cache file error: %w", err)
	}

	var cache Notify
	err = json.Unmarshal(b, &cache)
	if err != nil {
		return fmt.Errorf("unmarshal cache data error: %w", err)
	}

	if time.Now().Unix() > cache.TokenExpiresAt {
		return fmt.Errorf("token expired")
	}

	n.Token = cache.Token
	n.TokenExpiresAt = cache.TokenExpiresAt
	return nil
}

func (n *Notify) saveTokenCache() error {
	// 检查是否启用了令牌持久化
	if !n.TokenPersist {
		return fmt.Errorf("token persist not enabled")
	}

	// 将 Notify 对象序列化为 JSON
	b, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("marshal notify object failed: %w", err)
	}

	// 确保缓存目录存在
	cacheDir := filepath.Dir(n.CacheFilePath)
	if cacheDir != "." {
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return fmt.Errorf("create cache directory failed: %w", err)
		}
	}

	// 创建临时文件
	tempFile := n.CacheFilePath + ".tmp"
	f, err := os.Create(tempFile)
	if err != nil {
		return fmt.Errorf("create temp file failed: %w", err)
	}

	// 写入数据并关闭文件
	_, err = f.Write(b)
	if err != nil {
		f.Close()
		os.Remove(tempFile)
		return fmt.Errorf("write to temp file failed: %w", err)
	}

	if err = f.Sync(); err != nil {
		f.Close()
		os.Remove(tempFile)
		return fmt.Errorf("sync temp file failed: %w", err)
	}

	if err = f.Close(); err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("close temp file failed: %w", err)
	}

	// 原子性地重命名临时文件
	if err = os.Rename(tempFile, n.CacheFilePath); err != nil {
		return fmt.Errorf("rename temp file failed: %w", err)
	}

	return err
}

func (n *Notify) sendMessage(msgBody map[string]interface{}) (MessageResult, error) {
	var result MessageResult
	var client = &http.Client{Timeout: 10 * time.Second}

	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(msgBody)
	if err != nil {
		return result, fmt.Errorf("encode message error: %w", err)
	}
	res, err := client.Post(fmt.Sprintf("%s/message/send?access_token=%s", apiPrefix, n.Token), "application/json", body)
	if err != nil {
		return result, fmt.Errorf("send message request error: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	err = json.NewDecoder(res.Body).Decode(&result)
	if err != nil {
		return result, fmt.Errorf("send message result decode error: %w", err)
	}
	return result, nil
}

func (n *Notify) sendInternal(msgBody map[string]interface{}) (MessageResult, error) {
	var result MessageResult

	token, _, err := n.GetToken()
	if err != nil {
		return result, err
	}
	fmt.Println(token)
	result, err = n.sendMessage(msgBody)
	// 42001 access_token 已过期
	// 40014 不合法的access_token
	if err == nil && (result.ErrorCode == 42001 || result.ErrorCode == 40014) {
		// DONE check if error is token expire error, then retry once
		token, _, err := n.GetToken()
		fmt.Println(token)
		if err == nil {
			result, err = n.sendMessage(msgBody)
		}
	}

	return result, err
}
