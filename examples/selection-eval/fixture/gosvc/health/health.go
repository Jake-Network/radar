// Package health reports service liveness.
package health

func Status(dependenciesUp, dependenciesTotal int) string {
	switch {
	case dependenciesUp == dependenciesTotal:
		return "ok"
	case dependenciesUp == 0:
		return "down"
	}
	return "degraded"
}
