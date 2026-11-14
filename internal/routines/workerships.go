package routines

import "net/http"

// liveWorkerShips is the sessions of fleet's worker-launched ships that are underway or need you.
func (s *Service) liveWorkerShips(fleet string) ([]string, error) {
	recs, err := s.Store.Records(fleet)
	if err != nil {
		return nil, err
	}
	var listed []ListedShip
	if s.Listed != nil {
		listed = s.Listed()
	}
	var out []string
	for _, sh := range EventShips(fleet, recs, listed) {
		if !sh.Ended {
			out = append(out, sh.Session)
		}
	}
	return out, nil
}

// workerShipCounts is the live worker-launched ships of the fleet and of every fleet.
func (s *Service) workerShipCounts(fleet string) (here, all int, err error) {
	keys := s.Store.FleetKeys()
	if fleet != "" && !containsKey(keys, fleet) {
		keys = append(keys, fleet)
	}
	for _, k := range keys {
		live, err := s.liveWorkerShips(k)
		if err != nil {
			return 0, 0, err
		}
		all += len(live)
		if k == fleet {
			here = len(live)
		}
	}
	return here, all, nil
}

func containsKey(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// serveStopWorkerShips stops every live worker-launched ship of the fleet, or of every fleet; clones stay so Undo and Promote still work.
func (s *Service) serveStopWorkerShips(w http.ResponseWriter, r *http.Request) {
	keys := s.Store.FleetKeys()
	if key := r.PathValue("key"); key != "" {
		if _, err := s.Store.fleet(key); err != nil {
			fail(w, err)
			return
		}
		keys = []string{key}
	}
	stopped, failed := []string{}, []string{}
	for _, k := range keys {
		live, err := s.liveWorkerShips(k)
		if err != nil {
			fail(w, err)
			return
		}
		for _, session := range live {
			if s.Stop == nil || s.Stop(session) != nil {
				failed = append(failed, session)
			} else {
				stopped = append(stopped, session)
			}
		}
	}
	s.changed()
	writeJSON(w, http.StatusOK, map[string]any{"stopped": stopped, "failed": failed})
}
