package thebibites

func parseTransform(m map[string]any) Transform {
	var out Transform
	if m == nil {
		return out
	}
	if list, ok := listAt(m, "position"); ok && len(list) >= 2 {
		if x, ok := toFloat(list[0]); ok {
			out.PositionX = x
		}
		if y, ok := toFloat(list[1]); ok {
			out.PositionY = y
		}
	}
	if v, ok := floatAt(m, "rotation"); ok {
		out.Rotation = v
	}
	if v, ok := floatAt(m, "scale"); ok {
		out.Scale = v
	}
	return out
}

func parseRigidBody(m map[string]any) RigidBody {
	var out RigidBody
	if m == nil {
		return out
	}
	if v, ok := floatAt(m, "px"); ok {
		out.PX = v
	}
	if v, ok := floatAt(m, "py"); ok {
		out.PY = v
	}
	if v, ok := floatAt(m, "vx"); ok {
		out.VX = v
	}
	if v, ok := floatAt(m, "vy"); ok {
		out.VY = v
	}
	if v, ok := floatAt(m, "r"); ok {
		out.R = v
	}
	return out
}
