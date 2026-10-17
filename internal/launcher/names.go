package launcher

import (
	"math/rand/v2"
	"regexp"
	"strings"
)

// names are the ship names built into the binary. Ships cannot be renamed.
var names = []string{
	"Saltpaw", "Morning Tern", "Brass Kettle", "Little Bramble", "Gull Errand", "Tidecaller", "Kelpie",
	"Sea Biscuit", "Lantern Fish", "Puffin Post", "Otter Bell", "Driftwood Fox", "Marlin Spike", "Barnacle Bear",
	"Pebble Gull", "Windwhistle", "Copper Cormorant", "Tansy Tern", "Wavehopper", "Mossy Mackerel",
	"Salt Sparrow", "Anchor Wren", "Bramble Seal", "Harbor Hare", "Cinder Crab", "Foghorn Finch", "Ripple Robin",
	"Tumble Tide", "Starboard Stoat", "Quill Quokka", "Dory Dormouse", "Biscuit Barge", "Cobble Crane",
	"Mizzen Mouse", "Plover Pier", "Rope Rabbit", "Sandpiper Sue", "Skipper Snail", "Teacup Trawler",
	"Wobbly Walrus", "Yarrow Yawl", "Brine Badger", "Cockle Cat", "Dapple Dolphin", "Eddy Eel", "Fennel Ferry",
	"Gannet Grace", "Hazel Heron", "Ivy Ibis", "Jolly Jetty", "Kipper Kite", "Lobster Lamp", "Maple Mariner",
	"Nettle Narwhal", "Oyster Owl", "Puddle Puffin", "Quay Quail", "Rudder Rook", "Sorrel Swan",
	"Thimble Tugboat", "Umber Urchin", "Velvet Vole", "Whelk Whistle", "Yonder Yak", "Zephyr Zebu",
	"Amber Albatross", "Bobbin Boat", "Clover Clam", "Dune Duck", "Ember Egret", "Flint Fulmar", "Gorse Goose",
	"Heather Hull", "Inkwell Ibex", "Juniper Jay", "Knot Koala", "Larch Lugger", "Marsh Marten", "Nutmeg Newt",
	"Oakum Otter", "Pewter Petrel", "Quince Quay", "Reed Raven", "Sprat Skiff", "Thistle Thrush", "Umbrella Ray",
	"Vesper Vane", "Wicker Wherry", "Yew Yacht", "Zinc Zander", "Acorn Arc", "Bilge Bunny", "Cresset Crow",
	"Dabble Duck", "Eider Eddy", "Fathom Fawn", "Gimbal Gecko", "Hawser Hen", "Isle Impala", "Jetsam Jackdaw",
	"Keel Kingfisher", "Lagoon Lynx", "Mast Moth", "Nimbus Nutria", "Oar Ouzel", "Pennant Pika", "Quoy Quilt",
	"Rigging Roe", "Sextant Shrew", "Tiller Turtle", "Upwind Unicorn", "Vane Vixen", "Wake Wombat",
	"Yardarm Yeti", "Zigzag Zander", "Bosun Bee", "Capstan Cub", "Davit Dingo", "Ensign Elk", "Fender Frog",
	"Gaff Gull", "Halyard Hog", "Inlet Iguana", "Jib Jaguar", "Knarr Kid", "Lee Lemur", "Mooring Mole",
	"Nautilus Nibbler", "Oilskin Ocelot", "Painter Panda", "Quarterdeck Quail", "Reef Rabbit", "Shackle Sloth",
	"Topsail Tapir", "Underway Urial", "Vang Vulture", "Windlass Wolf", "Yawl Yapok", "Zenith Zorilla",
	"Pilot Pup", "Galley Goat", "Lanyard Lamb", "Binnacle Bat", "Cabin Cricket", "Dinghy Dove", "Skerry Skink",
	"Gunwale Gnu", "Sloop Swift", "Pontoon Pony", "Tern Tackle",
}

var roman = []struct {
	n int
	s string
}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
	{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}}

func numeral(n int) string {
	var b strings.Builder
	for _, r := range roman {
		for n >= r.n {
			b.WriteString(r.s)
			n -= r.n
		}
	}
	return b.String()
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug is the tmux session name for a ship name: "Saltpaw II" → "saltpaw-ii".
func Slug(name string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// pickName chooses a name at random from those not in use; when all are, it
// takes a random one and adds the lowest Roman numeral (from II) that is free.
// taken answers for a display name; sessions holds every tmux session name.
func pickName(taken func(name string) bool) string {
	free := func(n string) bool { return !taken(n) }
	var open []string
	for _, n := range names {
		if free(n) {
			open = append(open, n)
		}
	}
	if len(open) > 0 {
		return open[rand.IntN(len(open))]
	}
	base := names[rand.IntN(len(names))]
	for i := 2; ; i++ {
		if n := base + " " + numeral(i); free(n) {
			return n
		}
	}
}
