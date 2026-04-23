package nextcore

type ISRRoute struct {
	Path       string   `json:"path"`
	Tags       []string `json:"tags"`
	Revalidate int      `json:"revalidate"`
}

type TagPathMap struct {
	// tag -> list of cloudfront paths to invalidate
	Tags      map[string][]string `json:"tags"`
	Intervals map[string]int      `json:"intervals"`
}

func BuildTagMap(routes []ISRRoute) TagPathMap {
	m := TagPathMap{
		Tags:      make(map[string][]string),
		Intervals: make(map[string]int),
	}

	for _, r := range routes {
		m.Intervals[r.Path] = r.Revalidate
		for _, tag := range r.Tags {
			m.Tags[tag] = append(m.Tags[tag], r.Path)
		}
	}

	return m
}
