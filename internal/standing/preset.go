package standing

// PresetLimits is the Limit values of a preset; quiet hours are empty in both because the engineer sets them per fleet.
func PresetLimits(p Preset) Limits {
	l := Limits{
		SenderAllowlist: []string{}, RepoAllowlist: []string{}, WriteDeny: []string{}, QuietHours: []Window{},
		Skip:             Skip{Labels: []Scoped{}, Senders: []Scoped{}, DedupeKeys: []Scoped{}},
		WallClockMinutes: 60,
		QuotaHoldPercent: 90,
	}
	l.Ceilings.Fleet = Ceiling{Level: A5}
	l.Spend.LookoutCallUSD = 0.25
	l.Spend.ReviewerCallUSD = 0.10
	if p == Home {
		l.Ceilings.T1 = Ceiling{Level: A5}
		l.Ceilings.T2 = Ceiling{Level: A3, Restricted: DraftFix}
		l.Ceilings.T3 = Ceiling{Level: A1}
		l.Ceilings.T4 = Ceiling{Level: A3, Restricted: Investigate, Threshold: &Threshold{Events: 10, Days: 14, Level: A3, Restricted: DraftFix}}
		l.Rates = Rates{LaunchesPerHour: 4, LaunchesPerSenderPerDay: 3, Concurrent: 3}
		l.Spend.DailyWorkerUSD = 5
		return l
	}
	l.Ceilings.T1 = Ceiling{Level: A5, AsksFirst: true}
	l.Ceilings.T2 = Ceiling{Level: A1}
	l.Ceilings.T3 = Ceiling{Level: A0}
	l.Ceilings.T4 = Ceiling{Level: A3, Restricted: Investigate}
	l.Rates = Rates{LaunchesPerHour: 2, LaunchesPerSenderPerDay: 1, Concurrent: 2}
	l.Spend.DailyWorkerUSD = 10
	return l
}
