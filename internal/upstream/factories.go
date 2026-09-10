package upstream

import (
	"clash-of-tokens/internal/chatgptweb"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/providerdef"
	"clash-of-tokens/internal/providers/appdevice"
	"clash-of-tokens/internal/providers/businessweb"
	"clash-of-tokens/internal/providers/china"
	"clash-of-tokens/internal/providers/chinaapps"
	"clash-of-tokens/internal/providers/chinafinal"
	"clash-of-tokens/internal/providers/chinamore"
	"clash-of-tokens/internal/providers/chinanext"
	"clash-of-tokens/internal/providers/chinaremaining"
	"clash-of-tokens/internal/providers/coding"
	"clash-of-tokens/internal/providers/codingfinal"
	"clash-of-tokens/internal/providers/codingmore"
	"clash-of-tokens/internal/providers/codingnext"
	"clash-of-tokens/internal/providers/embedded"
	"clash-of-tokens/internal/providers/enterpriseweb"
	"clash-of-tokens/internal/providers/majorweb"
	"clash-of-tokens/internal/providers/playground"
	"clash-of-tokens/internal/providers/webhttp"
	"clash-of-tokens/internal/providers/webnext"
	"errors"
)

type factory func(config.Source, config.Browser, config.Device) *Client

var factories = map[string]factory{
	"http": func(s config.Source, _ config.Browser, _ config.Device) *Client { return newHTTP(s) },
	"chatgptweb": func(s config.Source, b config.Browser, _ config.Device) *Client {
		return &Client{source: s, web: chatgptweb.New(b, s.ID)}
	},
	"appdevice": func(s config.Source, _ config.Browser, d config.Device) *Client {
		return &Client{source: s, adapter: appdevice.New(s, d)}
	},
	"playground": func(s config.Source, b config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: playground.New(b)}
	},
	"chinaapps": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: chinaapps.New(s)}
	},
	"chinafinal": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: chinafinal.New(s)}
	},
	"chinamore": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: chinamore.New(s)}
	},
	"chinanext": func(s config.Source, b config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: chinanext.New(s, b)}
	},
	"chinaremaining": func(s config.Source, b config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: chinaremaining.New(s, b)}
	},
	"china": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: china.New(s)}
	},
	"coding": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: coding.New(s)}
	},
	"codingfinal": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: codingfinal.New(s)}
	},
	"codingmore": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: codingmore.New(s)}
	},
	"codingnext": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: codingnext.New(s)}
	},
	"embedded": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: embedded.New(s)}
	},
	"enterpriseweb": func(s config.Source, b config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: enterpriseweb.New(s, b)}
	},
	"businessweb": func(s config.Source, b config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: businessweb.New(s, b)}
	},
	"majorweb": func(s config.Source, b config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: majorweb.New(s, b)}
	},
	"webhttp": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: webhttp.New(s)}
	},
	"webnext": func(s config.Source, _ config.Browser, _ config.Device) *Client {
		return &Client{source: s, adapter: webnext.New(s)}
	},
}

func NewConfigured(s config.Source, b config.Browser, d config.Device) *Client {
	descriptor, ok := providerdef.Lookup(s.Adapter)
	if !ok || factories[descriptor.Factory] == nil {
		return &Client{source: s, initError: errors.New("unsupported provider adapter")}
	}
	return factories[descriptor.Factory](s, b, d)
}
func New(s config.Source, browser ...config.Browser) *Client {
	b := config.Default().Browser
	if len(browser) > 0 {
		b = browser[0]
	}
	return NewConfigured(s, b, config.Device{})
}
