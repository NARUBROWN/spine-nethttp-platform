package platform

import "github.com/NARUBROWN/spine"

// Option은 net/http 플랫폼을 Spine 앱에 연결합니다.
func Option(options ...NetHTTPOption) spine.Option {
	return spine.WithHTTPAdapter(NewFactory(options...))
}

// New는 net/http 플랫폼을 연결한 Spine 앱을 생성합니다.
func New(options ...NetHTTPOption) spine.App { return spine.New(Option(options...)) }
