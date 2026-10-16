package plex

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCatalogTitle(t *testing.T) {
	cases := []struct {
		in             Metadata
		id, guid, kind string
		title          string
	}{
		{Metadata{Type: "movie", GUID: "plex://movie/m1", Title: "Film"}, "m1", "plex://movie/m1", "movie", "Film"},
		{Metadata{Type: "clip", PrimaryGUID: "plex://movie/m2", Title: "Film 2"}, "m2", "plex://movie/m2", "movie", "Film 2"},
		{Metadata{Type: "season", GUID: "plex://season/s1", ParentGUID: "plex://show/sh", ParentTitle: "Serie", Title: "Season 3"}, "s1", "plex://show/sh", "show", "Serie"},
	}
	for _, c := range cases {
		got, ok := catalogTitle(&c.in)
		if !ok || got.ID != c.id || got.GUID != c.guid || got.Kind != c.kind || got.Title != c.title {
			t.Errorf("%s: got %+v", c.in.Type, got)
		}
	}
	if _, ok := catalogTitle(&Metadata{Type: "clip", PrimaryGUID: "plex://person/x", Title: "x"}); ok {
		t.Error("clips of other things must be skipped")
	}
}

func TestOwnDVRs(t *testing.T) {
	const body = `{"MediaContainer":{"Dvr":[
		{"key":"3","lineup":"lineup://tv.plex.providers.epg.xmltv/http%3A%2F%2Fother%3A5004%2Fguide.xml#x","Device":[{"uuid":"device://tv.plex.grabbers.hdhomerun/1053C0CA","deviceIdentifier":"1053C0CA"}]},
		{"key":7,"lineup":"lineup://tv.plex.providers.epg.xmltv/http://old-host:8080/epg.xml#Flimmerkiste","Device":[{"uuid":"device://tv.plex.grabbers.hdhomerun/a1b2c3d4"}]},
		{"key":"9","lineup":"lineup://tv.plex.providers.epg.xmltv/http%3A%2F%2Fflimmerkiste.lan%3A8080%2Fepg.xml#Flimmerkiste","Device":[{"deviceIdentifier":"FFFFFFFF"}],
		 "Lineup":[{"id":"lineup://tv.plex.providers.epg.xmltv/http%3A%2F%2Fflimmerkiste.lan%3A8080%2Fepg.xml#Flimmerkiste","title":"Flimmerkiste"}]}
	]}}`
	var r struct {
		MediaContainer struct {
			Dvr []DVR `json:"Dvr"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	dvrs := r.MediaContainer.Dvr
	if got := OwnDVRs(dvrs, "A1B2C3D4", ""); !reflect.DeepEqual(got, []string{"7"}) {
		t.Errorf("by device id: %v", got)
	}
	if got := OwnDVRs(dvrs, "A1B2C3D4", "http://flimmerkiste.lan:8080/"); !reflect.DeepEqual(got, []string{"7", "9"}) {
		t.Errorf("by device id or guide URL: %v", got)
	}
	if got := OwnDVRs(dvrs, "00000000", ""); len(got) != 0 {
		t.Errorf("foreign tuners only: %v", got)
	}
}

func TestFlexID(t *testing.T) {
	var v []Stream
	if err := json.Unmarshal([]byte(`[{"id":123},{"id":"abc"},{}]`), &v); err != nil {
		t.Fatal(err)
	}
	if v[0].ID != "123" || v[1].ID != "abc" || v[2].ID != "" {
		t.Errorf("%q %q %q", v[0].ID, v[1].ID, v[2].ID)
	}
}

// HDR videos carry their transfer characteristic for tone mapping (Plex: colorTrc).
func TestColorTransfer(t *testing.T) {
	var md Metadata
	body := `{"ratingKey":"1","type":"movie","title":"HDR","Media":[{"Part":[{"key":"/p","Stream":[
		{"id":1,"streamType":1,"index":0,"codec":"hevc","colorTrc":"smpte2084","colorPrimaries":"bt2020"},
		{"id":2,"streamType":2,"index":1,"codec":"eac3","languageCode":"ger"}]}]}]}`
	if err := json.Unmarshal([]byte(body), &md); err != nil {
		t.Fatal(err)
	}
	streams := ItemFrom(&md).Media[0].Parts[0].Streams
	if len(streams) != 2 || !streams[0].HDR() || streams[1].HDR() {
		t.Errorf("streams: %+v", streams)
	}
}
