package protocol

const DefaultEngine = "godot"

type Engine struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Implemented bool   `json:"implemented"`
}

var Engines = []Engine{
	{ID: DefaultEngine, Name: "Godot", Implemented: true},
	{ID: "unreal", Name: "Unreal Engine"},
	{ID: "unity", Name: "Unity"},
	{ID: "cocos", Name: "Cocos"},
}

func EngineID(id string) string {
	if id == "" {
		return DefaultEngine
	}
	return id
}

func FindEngine(id string) (Engine, bool) {
	for _, engine := range Engines {
		if engine.ID == id {
			return engine, true
		}
	}
	return Engine{}, false
}
