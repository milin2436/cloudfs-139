package cache

import (
	"time"

	"github.com/milin2436/cloudfs-139/cloudfso/cron"
	log "github.com/sirupsen/logrus"
)

var (
	cacheGcCron *cron.Cron
	gcFuncs     []func()
)

func init() {
	// TODO Move to bootstrap
	cacheGcCron = cron.NewCron(time.Hour)
	cacheGcCron.Do(func() {
		log.Infof("Start cache GC")
		for _, f := range gcFuncs {
			f()
		}
	})
}
