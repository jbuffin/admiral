package ledger

import "testing"

func TestCampaignCommitmentPicksTheOpenWorkCommitmentForThatCampaignOnly(t *testing.T) {
	open := []Commitment{
		{ID: "a", Trigger: Trigger{Kind: TriggerCampaign, Subject: "camp-2"}, Action: ActionWork},
		{ID: "b", Trigger: Trigger{Kind: "ship_done", Subject: "camp-1"}, Action: ActionWork},
		{ID: "c", Trigger: Trigger{Kind: TriggerCampaign, Subject: "camp-1"}, Action: ActionWork, Launched: 2, WaitingChildren: 1},
	}
	got, ok := CampaignCommitment(open, "camp-1")
	if !ok || got.ID != "c" || got.Launched != 2 || got.WaitingChildren != 1 {
		t.Errorf("CampaignCommitment(camp-1) = %+v, %v; want commitment c with its counts", got, ok)
	}
}

func TestCampaignCommitmentFindsNothingForAnotherCampaignOrANonCampaignCommitment(t *testing.T) {
	open := []Commitment{
		{ID: "a", Trigger: Trigger{Kind: TriggerCampaign, Subject: "camp-2"}, Action: ActionWork},
		{ID: "b", Trigger: Trigger{Kind: "ship_done", Subject: "camp-1"}, Action: ActionWork},
	}
	if got, ok := CampaignCommitment(open, "camp-1"); ok {
		t.Errorf("CampaignCommitment(camp-1) = %+v, true; want none", got)
	}
	if got, ok := CampaignCommitment(nil, "camp-1"); ok {
		t.Errorf("CampaignCommitment(nil) = %+v, true; want none", got)
	}
}
