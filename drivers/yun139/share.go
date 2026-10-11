package yun139

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"

	"github.com/milin2436/cloudfs-139/cloudfso/model"
	"github.com/milin2436/cloudfs-139/cloudfso/utils"
)

// The share ("外链") API is NOT on the AES share gateway this driver already
// uses for MetaShare accounts. It is served by the orchestration gateway,
// over the same mcloud-sign transport as the rest of the personal API, with
// plain JSON request and response bodies — no encryption at all.
//
// `hcy-cool-flag: 1` (set by shareHeaders for the AES gateway) switches the
// response to an AES-256-CBC base64 blob, which in turn forces the request
// body to be encrypted with a 32-byte literal key. That path works too, but it
// is strictly more moving parts for no benefit: the same call without the flag
// returns plain JSON. Do not send the flag here.
//
// Every per-action request object must carry commonAccountInfo INSIDE itself
// (not at the top level), or the gateway answers `1010010015 鉴权失效`.
const (
	outlinkHost = "https://yun.139.com"
	outlinkBase = outlinkHost + "/orchestration/personalCloud-rebuild/outlink/v1.0"
)

// Query kinds for ListOutLinks. 0 is rejected by the service with
// `1010000500`, so neither value is a sensible default.
const (
	ShareQryTypeSent     = 1 // 发出的分享
	ShareQryTypeReceived = 2 // 收到的分享
)

// ShareOptions controls a created outlink. Zero values give the most permissive
// share: no password, no expiry, public, no custom name.
type ShareOptions struct {
	// Encrypted adds a 4-character extract code. 139 generates the code — the
	// web client sends only the encrypt flag, never a code — so there is no
	// "set my own code" here. It comes back in ShareResult.Passwd.
	Encrypted bool
	// ExpiresDays is 0 (never expires), 1, or 7. Anything else is rejected
	// here rather than round-tripped to a server error.
	ExpiresDays int
	// Name is the link's display name shown in the share list. Empty uses
	// the driver default. Watch out: 139 scans names for sensitive words and
	// rejects matches with `9470`.
	Name string
	// PubType is 1 (public link) or 2 (friend-only). PubType 2 needs Viewers.
	PubType int
	// Viewers are phone numbers; required when PubType is 2.
	Viewers []string
	// SubLinkType is 1 for a "sub" link when 139's open-control switch is on.
	// Leave 0 unless you know you need it.
	SubLinkType int
}

// ShareResult is one created outlink.
type ShareResult struct {
	// LinkID addresses the link in DeleteOutLinks.
	LinkID string
	// URL is the shareable link.
	URL string
	// ObjID is the 139 object identifier the link points at.
	ObjID string
	// Passwd is the 4-character extract code, non-empty only for encrypted links.
	Passwd string
}

// ShareItem is one row of an outlink list. Counters are the 139 column set
// shown by the web UI.
type ShareItem struct {
	LinkID        string
	Name          string
	URL           string
	Passwd        string
	Encrypt       int64
	PubType       int64
	ObjType       int64
	DownloadTimes int64
	SaveTimes     int64
	BrowseTimes   int64
	CreateTime    string
	ExpireTime    string
	// Invalid is true when 139 has marked the link unusable.
	Invalid bool
	// Owner is the sharer's masked phone. ShareAuthor is populated on the
	// received side only.
	Owner       string
	ShareAuthor string
}

// ShareList is one page of ListOutLinks.
type ShareList struct {
	Items []ShareItem
	Count int64
	// Open is 139's raw openCount, which returned 0 for one live unexpired
	// share, so it is not a count of usable links.
	Open    int64
	BNum    int
	ENum    int
	QryType int
}

// jsonInt64 tolerates the 139 APIs, which return the same logical number as
// either 123 or "123" depending on the endpoint. A non-numeric value leaves
// the field at zero instead of failing the whole page.
type jsonInt64 int64

func (v *jsonInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*v = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	*v = jsonInt64(n)
	return nil
}

// outlinkAccount is the per-action auth block. It is also the only place an
// account is identified by phone rather than by the cookie Authorization.
type outlinkAccount struct {
	Account     string `json:"account"`
	AccountType int    `json:"accountType"`
}

type outlinkCreateReq struct {
	SubLinkType       int            `json:"subLinkType"`
	Encrypt           int            `json:"encrypt"`
	CatalogIDs        []string       `json:"caIDLst"`
	ContentIDs        []string       `json:"coIDLst"`
	PubType           int            `json:"pubType"`
	DedicatedName     string         `json:"dedicatedName"`
	Period            *int           `json:"period,omitempty"`
	PeriodUnit        int            `json:"periodUnit"`
	Viewers           []string       `json:"viewerLst"`
	ExtInfo           map[string]any `json:"extInfo"`
	CommonAccountInfo outlinkAccount `json:"commonAccountInfo"`
}

type outlinkListReq struct {
	CommonAccountInfo outlinkAccount `json:"commonAccountInfo"`
	NeedSetAccount    bool           `json:"needSetAccount"`
	Srt               int            `json:"srt"`
	SrtDr             int            `json:"srtDr"`
	BNum              int            `json:"bNum"`
	ENum              int            `json:"eNum"`
	QryType           int            `json:"qryType"`
}

type outlinkDelReq struct {
	CommonAccountInfo outlinkAccount `json:"commonAccountInfo"`
	LinkIDs           []string       `json:"linkIDs"`
}

type outlinkResult struct {
	ResultCode string `json:"resultCode"`
	ResultDesc string `json:"resultDesc"`
}

type outlinkCreateResp struct {
	BaseResp
	Data struct {
		Result        outlinkResult `json:"result"`
		GetOutLinkRes struct {
			GetOutLinkResSet []struct {
				LinkID  string `json:"linkID"`
				LinkURL string `json:"linkUrl"`
				ObjID   string `json:"objID"`
				Passwd  string `json:"passwd"`
			} `json:"getOutLinkResSet"`
		} `json:"getOutLinkRes"`
	} `json:"data"`
}

type outlinkItemResp struct {
	LinkID        string    `json:"linkID"`
	Name          string    `json:"lkName"`
	URL           string    `json:"url"`
	Passwd        string    `json:"passwd"`
	Encrypt       jsonInt64 `json:"encrypt"`
	PubType       jsonInt64 `json:"pubType"`
	ObjType       jsonInt64 `json:"objType"`
	DownloadTimes jsonInt64 `json:"dlTimes"`
	SaveTimes     jsonInt64 `json:"svTimes"`
	BrowseTimes   jsonInt64 `json:"brwTimes"`
	CreateTime    string    `json:"ctTime"`
	ExpireTime    string    `json:"expireTime"`
	Invalid       jsonInt64 `json:"isOutLinkInvalid"`
	Owner         string    `json:"owner"`
	ShareAuthor   string    `json:"shareAuthor"`
}

type outlinkListResp struct {
	BaseResp
	Data struct {
		Result           outlinkResult `json:"result"`
		GetOutLinkLstRes struct {
			Count    jsonInt64         `json:"count"`
			Open     jsonInt64         `json:"openCount"`
			OutLinks []outlinkItemResp `json:"outLinks"`
		} `json:"getOutLinkLstRes"`
	} `json:"data"`
}

// outlinkPost posts one action to the orchestration share gateway. The body is
// a single key wrapping the per-action request object, matching the service's
// convention: key `getOutLinkLstReq` → path `/getOutLinkList`.
func (d *Yun139) outlinkPost(path string, key string, req interface{}) ([]byte, error) {
	if err := d.checkShareSupport(); err != nil {
		return nil, err
	}
	payload := map[string]any{key: req}
	return d.newRequest(outlinkBase+path, http.MethodPost, func(r *resty.Request) {
		r.SetBody(payload)
	}, nil)
}

func (d *Yun139) checkShareSupport() error {
	switch d.Type {
	case MetaPersonalNew, MetaPersonal:
		return nil
	default:
		return fmt.Errorf(
			"sharing is not supported for account type %q (only personal accounts); "+
				"the 139 outlink gateway is a personal-cloud service", d.Type)
	}
}

// CreateOutLink shares one or more objects and returns the new link.
//
// Folders go into caIDLst, files into coIDLst — 139 rejects a mixed-up
// split. 139 caps one share at 50 objects.
func (d *Yun139) CreateOutLink(ctx context.Context, objs []model.Obj, opts ShareOptions) (*ShareResult, error) {
	if len(objs) == 0 {
		return nil, errors.New("nothing to share")
	}
	if len(objs) > 50 {
		return nil, fmt.Errorf("139 allows at most 50 objects per share, got %d", len(objs))
	}
	if opts.ExpiresDays != 0 && opts.ExpiresDays != 1 && opts.ExpiresDays != 7 {
		return nil, fmt.Errorf("expires must be 0 (never), 1 or 7 days, got %d", opts.ExpiresDays)
	}
	if opts.PubType == 0 {
		opts.PubType = 1
	}
	if opts.PubType != 1 && opts.PubType != 2 {
		return nil, fmt.Errorf("pubType must be 1 (public) or 2 (friends only), got %d", opts.PubType)
	}
	if opts.PubType == 2 && len(opts.Viewers) == 0 {
		return nil, errors.New("a friends-only share (pubType 2) needs at least one viewer")
	}
	if opts.Name == "" {
		opts.Name = "云盘分享"
	}

	caIDs := make([]string, 0, len(objs))
	coIDs := make([]string, 0, len(objs))
	for _, o := range objs {
		id := strings.TrimSpace(o.GetID())
		if id == "" {
			return nil, fmt.Errorf("object %q has no id; cannot share it", o.GetName())
		}
		if o.IsDir() {
			caIDs = append(caIDs, id)
		} else {
			coIDs = append(coIDs, id)
		}
	}

	// period is omitted entirely for a permanent link, matching the web client.
	var period *int
	if opts.ExpiresDays != 0 {
		days := opts.ExpiresDays
		period = &days
	}

	body := outlinkCreateReq{
		SubLinkType:       opts.SubLinkType,
		Encrypt:           0,
		CatalogIDs:        caIDs,
		ContentIDs:        coIDs,
		PubType:           opts.PubType,
		DedicatedName:     opts.Name,
		Period:            period,
		PeriodUnit:        1,
		Viewers:           opts.Viewers,
		ExtInfo:           map[string]any{"isWatermark": 0, "shareChannel": "3001"},
		CommonAccountInfo: outlinkAccount{Account: d.getAccount(), AccountType: 1},
	}
	if opts.Encrypted {
		body.Encrypt = 1
	}

	res, err := d.outlinkPost("/getOutLink", "getOutLinkReq", body)
	if err != nil {
		return nil, err
	}
	var out outlinkCreateResp
	if err := utils.Json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	set := out.Data.GetOutLinkRes.GetOutLinkResSet
	if len(set) == 0 {
		desc := out.Data.Result.ResultDesc
		if desc == "" {
			desc = out.Message
		}
		return nil, fmt.Errorf("139 created no outlink: %s", desc)
	}
	first := set[0]
	return &ShareResult{
		LinkID: first.LinkID,
		URL:    first.LinkURL,
		ObjID:  first.ObjID,
		Passwd: first.Passwd,
	}, nil
}

// ListOutLinks pages through the account's outlinks. qryType is
// ShareQryTypeSent or ShareQryTypeReceived; bNum/eNum are a 1-based inclusive
// page range. bNum<=0 defaults to the first page of eNum.
func (d *Yun139) ListOutLinks(ctx context.Context, qryType, bNum, eNum int) (*ShareList, error) {
	if bNum <= 0 {
		bNum = 1
	}
	if eNum <= 0 {
		eNum = 100
	}
	body := outlinkListReq{
		CommonAccountInfo: outlinkAccount{Account: d.getAccount(), AccountType: 1},
		NeedSetAccount:    true,
		Srt:               0,
		SrtDr:             0,
		BNum:              bNum,
		ENum:              eNum,
		QryType:           qryType,
	}
	res, err := d.outlinkPost("/getOutLinkList", "getOutLinkLstReq", body)
	if err != nil {
		return nil, err
	}
	var out outlinkListResp
	if err := utils.Json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	list := out.Data.GetOutLinkLstRes
	items := make([]ShareItem, 0, len(list.OutLinks))
	for _, in := range list.OutLinks {
		items = append(items, ShareItem{
			LinkID:        in.LinkID,
			Name:          in.Name,
			URL:           in.URL,
			Passwd:        in.Passwd,
			Encrypt:       int64(in.Encrypt),
			PubType:       int64(in.PubType),
			ObjType:       int64(in.ObjType),
			DownloadTimes: int64(in.DownloadTimes),
			SaveTimes:     int64(in.SaveTimes),
			BrowseTimes:   int64(in.BrowseTimes),
			CreateTime:    in.CreateTime,
			ExpireTime:    in.ExpireTime,
			Invalid:       in.Invalid != 0,
			Owner:         in.Owner,
			ShareAuthor:   in.ShareAuthor,
		})
	}
	return &ShareList{
		Items:   items,
		Count:   int64(list.Count),
		Open:    int64(list.Open),
		BNum:    bNum,
		ENum:    eNum,
		QryType: qryType,
	}, nil
}

// DeleteOutLinks cancels the given outlinks. 139 caps one call at 50.
func (d *Yun139) DeleteOutLinks(ctx context.Context, linkIDs []string) error {
	if len(linkIDs) == 0 {
		return errors.New("no link ids given")
	}
	if len(linkIDs) > 50 {
		return fmt.Errorf("139 allows at most 50 outlinks per cancellation, got %d", len(linkIDs))
	}
	body := outlinkDelReq{
		CommonAccountInfo: outlinkAccount{Account: d.getAccount(), AccountType: 1},
		LinkIDs:           linkIDs,
	}
	_, err := d.outlinkPost("/delOutLink", "delOutLinkReq", body)
	return err
}
