package campaigns

// Order is what the launcher gets for one child: the text after its skill, and the repo it clones.
type Order struct {
	Text      string
	CloneRepo string // host/owner/repo, as the hand route derives it from the child's node
}

// Read is a followed campaign as the commitment runner sees it on a pass.
type Read struct {
	ID       string
	Found    bool // false when the follow store no longer has the campaign
	Followed bool // false once the engineer stopped following, or the follow ended itself
	Campaign Campaign
	Orders   map[string]Order // by Child.URL, exactly as Decide returns it
}
