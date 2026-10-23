// Package all links in every built-in source adapter via blank imports.
// Import it for its side effect of populating the source registry:
//
//	import _ "github.com/PAumedes/polld/internal/source/all"
//
// Adding a new adapter means adding its import here — the single place
// that knows which adapters exist.
package all

import (
	_ "github.com/PAumedes/polld/internal/source/gitlab"
	_ "github.com/PAumedes/polld/internal/source/restapi"
)
