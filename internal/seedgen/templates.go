package seedgen

import (
	"fmt"
	"math/rand"
)

var (
	bookTitles = []string{
		"The Silent Ledger", "Granules of Time", "A Brief History of Indexes",
		"The Merge Tree", "Cardinality", "Shards", "The Bloom Filter",
		"Columnar", "Partition Key", "The Query Planner's Daughter",
	}
	restaurants = []string{
		"Golden Wok", "Pasta Villa", "Taco Loco", "The Green Bowl",
		"Sushi Stream", "Burger Junction", "Curry House", "Falafel Bar",
	}
	dishes = []string{
		"pad thai", "margherita pizza", "chicken burrito", "quinoa salad",
		"salmon nigiri", "veggie burger", "chicken tikka masala", "hummus plate",
	}
	devices            = []string{"ios", "android", "chrome", "firefox", "safari", "edge"}
	currencies         = []string{"USD", "EUR", "GBP", "JPY", "CAD"}
	plans              = []string{"free", "starter", "pro", "enterprise"}
	paymentMethodTypes = []string{"card", "paypal", "bank_transfer", "apple_pay", "google_pay"}
	referrers          = []string{"google", "twitter", "direct", "newsletter", "partner-blog", "reddit"}
	urls               = []string{
		"/", "/pricing", "/docs", "/blog/clickhouse-indexes", "/checkout",
		"/dashboard", "/settings", "/signup", "/product/widgets",
	}
	endpoints = []string{
		"/api/v1/events", "/api/v1/users", "/api/v1/orders", "/api/v1/search",
		"/api/v1/billing", "/api/v1/webhooks",
	}
	statusCodes   = []string{"200", "201", "204", "400", "401", "404", "429", "500", "503"}
	methods       = []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	severities    = []string{"low", "medium", "high", "critical"}
	errorMessages = []string{
		"connection reset by peer", "timeout waiting for upstream",
		"invalid json payload", "permission denied", "rate limit exceeded",
		"nil pointer dereference", "context deadline exceeded",
	}
	elements       = []string{"nav.signup", "hero.cta", "footer.link", "cart.checkout", "pricing.upgrade"}
	carriers       = []string{"ups", "fedex", "usps", "dhl"}
	shipStatuses   = []string{"label_created", "in_transit", "out_for_delivery", "delivered", "exception"}
	cities         = []string{"Seattle", "Austin", "Denver", "Chicago", "Miami", "Boston", "Phoenix", "Portland"}
	paymentStatus  = []string{"succeeded", "failed", "pending", "refunded"}
	reviewComments = []string{
		"Exactly as described, fast shipping.", "Great value for the price.",
		"Not what I expected, but still useful.", "Would buy again.",
		"Arrived damaged, requested a refund.", "Customer support was excellent.",
	}
)

// buildTemplates returns one generator function per synthetic event type.
// Each uses a different JSON key for "the person" (owner_id, eater_id,
// buyer_id, ...) — see the Generator.person doc comment for why.
func (g *Generator) buildTemplates() []template {
	return []template{
		{"book", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"owner_id": g.person(),
				"title":    choice(r, bookTitles),
				"isbn":     fmt.Sprintf("978-%09d", r.Intn(999999999)),
				"price":    round2(r.Float64()*40 + 5),
				"pages":    r.Intn(500) + 80,
			}
		}},
		{"lunch", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"eater_id":   g.person(),
				"restaurant": choice(r, restaurants),
				"dish":       choice(r, dishes),
				"price":      round2(r.Float64()*25 + 5),
				"calories":   r.Intn(1200) + 200,
			}
		}},
		{"login", func(r *rand.Rand, g *Generator) map[string]any {
			personID := g.person()
			return map[string]any{
				"user_id": personID,
				"ip":      randomIP(r),
				"device":  choice(r, devices),
				"success": r.Float64() > 0.1,
				// A nested object alongside the flat user_id: real payloads
				// commonly carry both a top-level foreign key and a fuller
				// embedded sub-object. This is what flattens to dot-notation
				// attribute keys like user.id / user.is_active / user.plan
				// (see internal/flatten), queryable as user.is_active:true.
				"user": map[string]any{
					"id":        personID,
					"is_active": r.Float64() > 0.12,
					"plan":      choice(r, plans),
				},
			}
		}},
		{"purchase", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"buyer_id": g.person(),
				"sku":      fmt.Sprintf("SKU-%04d", r.Intn(9999)),
				"amount":   round2(r.Float64()*300 + 1),
				"currency": choice(r, currencies),
				// Nested payment_method -> payment_method.type / .last4 /
				// .verified attribute keys.
				"payment_method": map[string]any{
					"type":     choice(r, paymentMethodTypes),
					"last4":    fmt.Sprintf("%04d", r.Intn(9999)),
					"verified": r.Float64() > 0.05,
				},
			}
		}},
		{"signup", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"user_id":  g.person(),
				"plan":     choice(r, plans),
				"referrer": choice(r, referrers),
			}
		}},
		{"page_view", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"visitor_id":  g.person(),
				"url":         choice(r, urls),
				"referrer":    choice(r, referrers),
				"duration_ms": r.Intn(60000),
			}
		}},
		{"api_call", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"caller_id":   g.person(),
				"endpoint":    choice(r, endpoints),
				"method":      choice(r, methods),
				"status_code": choice(r, statusCodes),
				"latency_ms":  r.Intn(2000),
			}
		}},
		{"payment", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"payer_id": g.person(),
				"amount":   round2(r.Float64()*500 + 1),
				"currency": choice(r, currencies),
				"status":   choice(r, paymentStatus),
			}
		}},
		{"error", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"user_id":  g.person(),
				"message":  choice(r, errorMessages),
				"severity": choice(r, severities),
				"stack":    []string{"main.handler", "main.middleware", "runtime.goexit"},
			}
		}},
		{"click", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"clicker_id": g.person(),
				"element":    choice(r, elements),
				"page":       choice(r, urls),
			}
		}},
		{"shipment", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"recipient_id":    g.person(),
				"carrier":         choice(r, carriers),
				"tracking_number": fmt.Sprintf("TRK%09d", r.Intn(999999999)),
				"status":          choice(r, shipStatuses),
				"address": map[string]any{
					"city": choice(r, cities),
					"zip":  fmt.Sprintf("%05d", r.Intn(99999)),
				},
			}
		}},
		{"review", func(r *rand.Rand, g *Generator) map[string]any {
			return map[string]any{
				"reviewer_id": g.person(),
				"product_id":  fmt.Sprintf("PROD-%04d", r.Intn(500)),
				"rating":      r.Intn(5) + 1,
				"comment":     choice(r, reviewComments),
			}
		}},
	}
}
