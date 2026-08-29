package kit

// wordlist backs the six-word verification code. The code is NOT a secret
// and carries no key material — it exists so the operator must physically
// hold the printed kit and type six words back, proving the kit was saved
// before backups begin (BK-6 activation gate). 256 words, all common,
// unambiguous to read aloud over a phone, no near-homophones.
var wordlist = [256]string{
	"acorn", "amber", "anchor", "apple", "april", "arrow", "atlas", "autumn",
	"bacon", "badge", "bagel", "banjo", "barn", "basil", "beach", "beacon",
	"beaver", "bell", "berry", "bison", "blanket", "blossom", "bluff", "boat",
	"bonfire", "book", "boot", "bottle", "bridge", "brook", "broom", "bucket",
	"butter", "button", "cabin", "cactus", "camera", "candle", "canoe", "canyon",
	"carpet", "carrot", "castle", "cedar", "chair", "chalk", "cherry", "chess",
	"chimney", "cider", "circle", "clover", "coast", "cobalt", "coffee", "comet",
	"compass", "copper", "coral", "cotton", "cradle", "crayon", "cricket", "crystal",
	"daisy", "deer", "denim", "desk", "diamond", "dolphin", "donut", "door",
	"dragon", "drum", "eagle", "easel", "echo", "elbow", "elm", "ember",
	"engine", "fabric", "falcon", "feather", "fence", "fern", "fiddle", "field",
	"finch", "flag", "flame", "flute", "forest", "fossil", "fountain", "fox",
	"frost", "garden", "garlic", "gate", "gecko", "ginger", "glacier", "glove",
	"goose", "granite", "grape", "guitar", "hammer", "hammock", "harbor", "harvest",
	"hazel", "heron", "hill", "honey", "horse", "hotel", "igloo", "inkwell",
	"island", "ivory", "jacket", "jaguar", "jasmine", "jelly", "jigsaw", "juniper",
	"kayak", "kettle", "kite", "koala", "ladder", "lantern", "laurel", "lemon",
	"lily", "lime", "lizard", "llama", "lobster", "locket", "lotus", "lumber",
	"magnet", "mango", "maple", "marble", "meadow", "melon", "mesa", "mint",
	"mirror", "mitten", "monkey", "moon", "moss", "mountain", "mural", "mushroom",
	"napkin", "needle", "nickel", "north", "nutmeg", "oak", "oasis", "ocean",
	"olive", "onion", "opal", "orange", "orbit", "orchard", "otter", "owl",
	"paddle", "palm", "panda", "paper", "parrot", "peach", "pearl", "pebble",
	"pencil", "penguin", "pepper", "piano", "pickle", "pigeon", "pillow", "pine",
	"planet", "plum", "pocket", "pond", "poplar", "prairie", "prism", "pumpkin",
	"puzzle", "quartz", "quill", "quilt", "rabbit", "raccoon", "radish", "raft",
	"rainbow", "raven", "reef", "ribbon", "river", "robin", "rocket", "rooster",
	"rose", "ruby", "saddle", "salmon", "sandal", "sapphire", "scarf", "seed",
	"shadow", "shell", "ship", "shovel", "silver", "sled", "slipper", "smoke",
	"spark", "spider", "spoon", "spring", "spruce", "squash", "stable", "star",
	"stone", "stream", "sugar", "summer", "sunset", "swan", "table", "tulip",
	"turtle", "valley", "velvet", "violet", "wagon", "walnut", "willow", "winter",
}
