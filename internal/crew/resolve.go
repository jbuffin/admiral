package crew

type Input struct {
	Admiral bool
	Layers  map[string]string
	Run     string
	Repo    string
}

type Result struct{}

func Resolve(in Input) Result { return Result{} }
