package hybrid_cache

import (
	"io"

	"github.com/milin2436/cloudfs-139/cloudfso/buffer"
)

type BackingStore interface {
	buffer.Block
	io.Closer
	GrowTo(size int64) error
}
