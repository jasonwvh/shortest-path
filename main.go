package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"

	_ "github.com/lib/pq"
)

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "changeme"
	dbname   = "osm"
)

func main() {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// alterTableSQL := `
	// 	ALTER TABLE planet_osm_roads ADD COLUMN source integer;
	// 	ALTER TABLE planet_osm_roads ADD COLUMN target integer;
	// `
	// _, err = db.Exec(alterTableSQL)
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// initPgRoutingSQL := `
	// 	SELECT pgr_createTopology('planet_osm_roads', 0.00001, 'way', 'osm_id');
	// 	SELECT pgr_analyzeGraph('planet_osm_roads', 0.00001, 'way', 'osm_id');
	// `
	// _, err = db.Exec(initPgRoutingSQL)
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// fmt.Println("initialization complete")

	//shortestPathSQL := `
	//	SELECT r.osm_id, r.way, w.tags
	//	FROM planet_osm_roads r
	//	JOIN (SELECT * FROM pgr_dijkstra(
	//	'SELECT osm_id AS id,
	//		source,
	//		target,
	//		ST_Length(way) AS cost,
	//		-ST_Length(way) AS reverse_cost
	//		FROM planet_osm_roads',
	//	104606, 110071,
	//	directed := false)) sp
	//	ON r.osm_id = sp.edge
	//	JOIN planet_osm_ways w ON w.id = r.osm_id;`

	findCoordSQL := `
		SELECT
		ST_X(ST_Transform (way, 3857)) AS longitude,
		ST_Y(ST_Transform (way, 3857)) AS latitude
		FROM planet_osm_point
		WHERE osm_id = 2043147561
	`

	rows, err := db.Query(findCoordSQL)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var coord Coordinate
	if rows.Next() {
		if err := rows.Scan(&coord.Longitude, &coord.Latitude); err != nil {
			log.Fatal(err)
		}
	}

	findRoadSQL := fmt.Sprintf(`
		SELECT id FROM planet_osm_roads_vertices_pgr 
		ORDER BY the_geom <-> (SELECT ST_SetSRID(ST_MakePoint(%f, %f), 3857)) 
		LIMIT 1;
	`, coord.Longitude, coord.Latitude)

	rows, err = db.Query(findRoadSQL)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var id string
	if rows.Next() {
		if err := rows.Scan(&id); err != nil {
			log.Fatal(err)
		}
	}

	multinodeShortestPathSQL := `
		SELECT r.osm_id, r.tags, ST_AsGeoJSON(r.way), r.source, sp.cost, sp.agg_cost
		FROM planet_osm_roads r
		JOIN (SELECT * FROM pgr_TSP(
		  $$SELECT * FROM pgr_dijkstraCostMatrix(
			'SELECT osm_id AS id, source, target, ST_Length(way) AS cost, -ST_Length(way) AS reverse_cost FROM planet_osm_roads',
			(SELECT array_agg(id) FROM planet_osm_roads_vertices_pgr WHERE id IN (542, 524, 552)),
			directed => false) $$)) sp
		ON sp.node = r.source;
	`

	rows, err = db.Query(multinodeShortestPathSQL)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var roads []Road
	for rows.Next() {
		var road Road
		if err := rows.Scan(&road.OSMID, &road.Tags, &road.Way, &road.Source, &road.Cost, &road.AggCost); err != nil {
			log.Fatal(err)
		}
		roads = append(roads, road)
	}

	if len(roads) > 0 {
		roads = roads[:len(roads)-1] // Remove the last element
	}

	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	for _, road := range roads {
		fmt.Printf("osm_id: %, ways: %s, tags: %s\n", road.OSMID, road.Way, road.Tags)
	}

	tmpl := template.Must(template.New("map.gohtml").ParseFiles("map.gohtml"))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.Execute(w, struct{ Roads []Road }{roads})
		if err != nil {
			log.Fatal(err)
		}
	})
	log.Println("Starting server on :9999")
	log.Fatal(http.ListenAndServe(":9999", nil))
}

type Road struct {
	OSMID   int
	Name    string
	Way     string
	Tags    string
	Source  string
	Cost    float64
	AggCost float64
}

type Coordinate struct {
	Longitude float64
	Latitude  float64
}
