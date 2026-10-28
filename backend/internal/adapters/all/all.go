// Package all exists so that every adapter is registered. A package's init runs
// only when something imports it, so the places that need the whole registry,
// the server and the catalog validation, import this one package.
package all

import (
	_ "downradar.info/backend/internal/adapters/auth0"
	_ "downradar.info/backend/internal/adapters/betterstack"
	_ "downradar.info/backend/internal/adapters/cronjob"
	_ "downradar.info/backend/internal/adapters/flashduty"
	_ "downradar.info/backend/internal/adapters/lark"
	_ "downradar.info/backend/internal/adapters/microsoft"
	_ "downradar.info/backend/internal/adapters/midtrans"
	_ "downradar.info/backend/internal/adapters/opencode"
	_ "downradar.info/backend/internal/adapters/pagerduty"
	_ "downradar.info/backend/internal/adapters/paypal"
	_ "downradar.info/backend/internal/adapters/ping"
	_ "downradar.info/backend/internal/adapters/railway"
	_ "downradar.info/backend/internal/adapters/slack"
	_ "downradar.info/backend/internal/adapters/solarwinds"
	_ "downradar.info/backend/internal/adapters/sorryapp"
	_ "downradar.info/backend/internal/adapters/statuscake"
	_ "downradar.info/backend/internal/adapters/statusio"
	_ "downradar.info/backend/internal/adapters/statuspage"
	_ "downradar.info/backend/internal/adapters/uptimerobot"
)
