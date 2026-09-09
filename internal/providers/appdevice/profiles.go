// Package appdevice ports FreeCoding's three Android application workflows to Go.
// Reference: Damue01/FreeCoding f0ae52075e584bd55989ec4053b5ff61c09e422f, MIT.
package appdevice

type Profile struct {
	ID, Model, Package string
	Width, Height      int
}

var profiles = map[string]Profile{
	"meituan-xiaotuan":  {ID: "meituan-xiaotuan", Model: "meituan_xiaotuan", Package: "com.sankuai.meituan"},
	"wangzhe-lingbao":   {ID: "wangzhe-lingbao", Model: "wangzhe_lingbao", Package: "com.tencent.tmgp.sgame", Width: 3200, Height: 1440},
	"douyin-xiaohuoren": {ID: "douyin-xiaohuoren", Model: "douyin_xiaohuoren", Package: "my.maya.android", Width: 1440, Height: 3200},
}
