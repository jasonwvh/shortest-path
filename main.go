package main

import (
	"database/sql"
	"fmt"
	"log"

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

	shortestPathSQL := `
		SELECT r.osm_id, r.way, w.tags
		FROM planet_osm_roads r
		JOIN (SELECT * FROM pgr_dijkstra(
		'SELECT osm_id AS id,
			source,
			target,
			ST_Length(way) AS cost,
			-ST_Length(way) AS reverse_cost
			FROM planet_osm_roads',
		104606, 110071,
		directed := false)) sp
		ON r.osm_id = sp.edge
		JOIN planet_osm_ways w ON w.id = r.osm_id;`

	rows, err := db.Query(shortestPathSQL)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var roads []Road
	for rows.Next() {
		var road Road
		if err := rows.Scan(&road.OSMID, &road.Way, &road.Tags); err != nil {
			log.Fatal(err)
		}
		roads = append(roads, road)
	}

	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	for _, road := range roads {
		fmt.Printf("osm_id: %d, way: %s, tags: %s\n", road.OSMID, road.Way, road.Tags)
	}
}

type Road struct {
	OSMID int
	Way   string
	Tags  string
}
