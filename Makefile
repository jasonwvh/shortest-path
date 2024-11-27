import:
	osm2pgsql -d osm -U postgres -H localhost --password --create --slim -C 2000 --hstore --multi-geometry data/malaysia-singapore-brunei-latest.osm.pbf

