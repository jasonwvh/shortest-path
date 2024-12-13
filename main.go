package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	_ "github.com/lib/pq"
)

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "changeme"
	dbname   = "osm"
)

func initDb() (*sql.DB, error) {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return nil, err
	}

	return db, nil
}

func initTopology(db *sql.DB) error {
	alterTableSQL := `
		ALTER TABLE planet_osm_roads ADD COLUMN source integer;
		ALTER TABLE planet_osm_roads ADD COLUMN target integer;
	`
	_, err := db.Exec(alterTableSQL)
	if err != nil {
		return err
	}

	initPgRoutingSQL := `
		SELECT pgr_createTopology('planet_osm_roads', 0.00001, 'way', 'osm_id');
		SELECT pgr_analyzeGraph('planet_osm_roads', 0.00001, 'way', 'osm_id');
	`
	_, err = db.Exec(initPgRoutingSQL)
	if err != nil {
		return err
	}

	fmt.Println("initialization complete")
	return nil
}

type Point struct {
	ID   int
	Name string
}

func findOsmIDByName(db *sql.DB, name string) ([]Point, error) {
	findOsmIDSQL := `
		SELECT osm_id, name
		FROM planet_osm_point
		WHERE name LIKE '%' || $1 || '%'
		LIMIT 10;
	`

	rows, err := db.Query(findOsmIDSQL, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []Point
	for rows.Next() {
		var point Point
		if err := rows.Scan(&point.ID, &point.Name); err != nil {
			return nil, err
		}
		points = append(points, point)
	}

	return points, nil
}

func findCoords(db *sql.DB, id int) (Coordinate, error) {
	findCoordSQL := `
		SELECT
		ST_X(ST_Transform(way, 3857)) AS longitude,
		ST_Y(ST_Transform(way, 3857)) AS latitude
		FROM planet_osm_point
		WHERE osm_id = $1
	`

	rows, err := db.Query(findCoordSQL, id)
	if err != nil {
		return Coordinate{}, err
	}
	defer rows.Close()

	var coord Coordinate
	if rows.Next() {
		if err := rows.Scan(&coord.Longitude, &coord.Latitude); err != nil {
			return Coordinate{}, err
		}
	}

	return coord, nil
}

func findRoads(db *sql.DB, coord Coordinate) (string, error) {
	findRoadSQL := fmt.Sprintf(`
		SELECT id FROM planet_osm_roads_vertices_pgr 
		ORDER BY the_geom <-> (SELECT ST_SetSRID(ST_MakePoint(%f, %f), 3857)) 
		LIMIT 1;
	`, coord.Longitude, coord.Latitude)

	rows, err := db.Query(findRoadSQL)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var id string
	if rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
	}

	return id, nil
}

func findShortestPath(db *sql.DB, source string, target string) ([]Road, error) {
	shortestPathSQL := fmt.Sprintf(`
		SELECT r.osm_id, ST_AsGeoJSON(r.way), w.tags
		FROM planet_osm_roads r
		JOIN (SELECT * FROM pgr_dijkstra(
		'SELECT osm_id AS id,
			source,
			target,
			ST_Length(way) AS cost,
			-ST_Length(way) AS reverse_cost
			FROM planet_osm_roads',
		%s, %s,
		directed := false)) sp
		ON r.osm_id = sp.edge
		JOIN planet_osm_ways w ON w.id = r.osm_id;`, source, target)

	rows, err := db.Query(shortestPathSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roads []Road
	for rows.Next() {
		var road Road
		if err := rows.Scan(&road.OSMID, &road.Way, &road.Tags); err != nil {
			return nil, err
		}
		roads = append(roads, road)
	}

	if len(roads) > 0 {
		roads = roads[:len(roads)-1] // Remove the last element
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return roads, nil
}

func findBestRoute(db *sql.DB, nodeIDs []int) ([]Road, error) {
	ids := make([]string, len(nodeIDs))
	for i, id := range nodeIDs {
		ids[i] = fmt.Sprintf("%d", id)
	}
	idsStr := strings.Join(ids, ",")

	multinodeShortestPathSQL := fmt.Sprintf(`
		SELECT r.osm_id, r.tags, ST_AsGeoJSON(r.way), r.source, sp.cost, sp.agg_cost
		FROM planet_osm_roads r
		JOIN (SELECT * FROM pgr_TSP(
		  $$SELECT * FROM pgr_dijkstraCostMatrix(
			'SELECT osm_id AS id, source, target, ST_Length(way) AS cost, -ST_Length(way) AS reverse_cost FROM planet_osm_roads',
			(SELECT array_agg(id) FROM planet_osm_roads_vertices_pgr WHERE id IN (%s)),
			directed => false) $$)) sp
		ON sp.node = r.source;
	`, idsStr)

	rows, err := db.Query(multinodeShortestPathSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roads []Road
	for rows.Next() {
		var road Road
		if err := rows.Scan(&road.OSMID, &road.Tags, &road.Way, &road.Source, &road.Cost, &road.AggCost); err != nil {
			return nil, err
		}
		roads = append(roads, road)
	}

	if len(roads) > 0 {
		roads = roads[:len(roads)-1] // Remove the last element
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return roads, nil
}

func main() {
	db, err := initDb()
	if err != nil {
		return
	}

	tmpl := template.Must(template.New("map.gohtml").ParseFiles("map.gohtml"))

	http.HandleFunc("/lookup", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name parameter is required", http.StatusBadRequest)
			return
		}

		points, err := findOsmIDByName(db, name)
		if err != nil {
			http.Error(w, "Error finding osm_id by name", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(points)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		osmIDs := r.URL.Query()["osm_id"]
		if len(osmIDs) == 0 {
			err = tmpl.Execute(w, struct{ Roads []Road }{})
			return
		}

		var nodeIDs []int
		for _, osmIDStr := range osmIDs {
			osmID, err := strconv.Atoi(osmIDStr)
			if err != nil {
				http.Error(w, "Invalid osm_id parameter", http.StatusBadRequest)
				return
			}

			coord, err := findCoords(db, osmID)
			if err != nil {
				http.Error(w, "Error finding coordinates", http.StatusInternalServerError)
				return
			}

			id, err := findRoads(db, coord)
			if err != nil {
				http.Error(w, "Error finding roads", http.StatusInternalServerError)
				return
			}

			nodeID, err := strconv.Atoi(id)
			if err != nil {
				http.Error(w, "Error converting road ID", http.StatusInternalServerError)
				return
			}

			nodeIDs = append(nodeIDs, nodeID)
		}

		routes, err := findBestRoute(db, nodeIDs)
		if err != nil {
			http.Error(w, "Error finding shortest path", http.StatusInternalServerError)
			return
		}

		var allRoads []Road
		for i := 0; i < len(routes)-1; i++ {
			source := routes[i].Source
			target := routes[i+1].Source

			roads, err := findShortestPath(db, source, target)
			if err != nil {
				return
			}

			allRoads = append(allRoads, roads...)
		}

		err = tmpl.Execute(w, struct{ Roads []Road }{allRoads})
		if err != nil {
			http.Error(w, "Error rendering template", http.StatusInternalServerError)
			return
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
