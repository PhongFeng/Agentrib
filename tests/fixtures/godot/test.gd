extends SceneTree

func _initialize() -> void:
	var marker := FileAccess.get_file_as_string("res://qa_marker.txt").strip_edges()
	if marker == "AGENTRIB_CANCEL":
		print("Cancellation fixture is running")
		OS.delay_msec(120000)
	if marker != "AGENTRIB_ACCEPTANCE":
		push_error("Expected the marker in the exact development commit")
		quit(1)
		return
	print("PASS: checked-out development revision contains the acceptance marker")
	quit(0)
