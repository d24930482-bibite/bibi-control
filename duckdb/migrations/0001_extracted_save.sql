CREATE TABLE IF NOT EXISTS save_archives (
	save_id TEXT,
	source_path TEXT,
	file_name TEXT,
	size_bytes BIGINT,
	sha256 TEXT
);

CREATE TABLE IF NOT EXISTS save_entries (
	save_id TEXT,
	entry_index INTEGER,
	entry_name TEXT,
	kind TEXT,
	sha256 TEXT,
	compressed_size BIGINT,
	uncompressed_size BIGINT,
	has_utf8_bom BOOLEAN
);

CREATE TABLE IF NOT EXISTS diagnostics (
	save_id TEXT,
	entry_name TEXT,
	severity TEXT,
	code TEXT,
	message TEXT
);

CREATE TABLE IF NOT EXISTS scenes (
	save_id TEXT,
	entry_name TEXT,
	version TEXT,
	simulated_time DOUBLE,
	has_simulated_time BOOLEAN,
	reported_n_bibites BIGINT,
	has_reported_n_bibites BOOLEAN,
	reported_n_pellets BIGINT,
	has_reported_n_pellets BOOLEAN,
	parsed_bibites INTEGER,
	parsed_eggs INTEGER,
	alive_bibites INTEGER,
	dead_bibites INTEGER,
	dying_bibites INTEGER,
	parsed_pellets INTEGER
);

CREATE TABLE IF NOT EXISTS vars (
	save_id TEXT,
	entry_name TEXT,
	tower_max_id BIGINT,
	has_tower_max_id BOOLEAN
);

CREATE TABLE IF NOT EXISTS scene_color_selectors (
	save_id TEXT,
	entry_name TEXT,
	color_selector_index INTEGER,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS scene_phero_towers (
	save_id TEXT,
	entry_name TEXT,
	tower_kind TEXT,
	tower_index INTEGER,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS scene_rad_towers (
	save_id TEXT,
	entry_name TEXT,
	tower_kind TEXT,
	tower_index INTEGER,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_simulation_values (
	save_id TEXT,
	entry_name TEXT,
	scope TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	setting_name TEXT,
	path TEXT,
	value_type TEXT,
	number_value DOUBLE,
	string_value TEXT,
	bool_value BOOLEAN,
	raw_json TEXT,
	wrapper_raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_independent_values (
	save_id TEXT,
	entry_name TEXT,
	scope TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	setting_name TEXT,
	path TEXT,
	value_type TEXT,
	number_value DOUBLE,
	string_value TEXT,
	bool_value BOOLEAN,
	raw_json TEXT,
	wrapper_raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_materials (
	save_id TEXT,
	entry_name TEXT,
	material_index INTEGER,
	material_name TEXT,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_material_values (
	save_id TEXT,
	entry_name TEXT,
	scope TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	setting_name TEXT,
	path TEXT,
	value_type TEXT,
	number_value DOUBLE,
	string_value TEXT,
	bool_value BOOLEAN,
	raw_json TEXT,
	wrapper_raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_zones (
	save_id TEXT,
	entry_name TEXT,
	zone_index INTEGER,
	zone_id BIGINT,
	has_zone_id BOOLEAN,
	name TEXT,
	material TEXT,
	distribution TEXT,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_zone_geometry (
	save_id TEXT,
	entry_name TEXT,
	zone_index INTEGER,
	zone_id BIGINT,
	has_zone_id BOOLEAN,
	geometry_index INTEGER,
	geometry_kind TEXT,
	position_x DOUBLE,
	position_y DOUBLE,
	radius DOUBLE,
	radius_is_relative BOOLEAN,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_zone_values (
	save_id TEXT,
	entry_name TEXT,
	scope TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	setting_name TEXT,
	path TEXT,
	value_type TEXT,
	number_value DOUBLE,
	string_value TEXT,
	bool_value BOOLEAN,
	raw_json TEXT,
	wrapper_raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_zone_groups (
	save_id TEXT,
	entry_name TEXT,
	group_index INTEGER,
	name TEXT,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_bibite_spawners (
	save_id TEXT,
	entry_name TEXT,
	spawner_index INTEGER,
	path TEXT,
	spawn_priority DOUBLE,
	minimum DOUBLE,
	randomize_genes TEXT,
	growth_at_spawn TEXT,
	tagging TEXT,
	spawn_type TEXT,
	total_spawned BIGINT,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_changers (
	save_id TEXT,
	entry_name TEXT,
	changer_index INTEGER,
	name TEXT,
	repeat_enabled BOOLEAN,
	start_time DOUBLE,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS settings_changer_points (
	save_id TEXT,
	entry_name TEXT,
	changer_index INTEGER,
	point_index INTEGER,
	t DOUBLE,
	y DOUBLE,
	d TEXT,
	f DOUBLE
);

CREATE TABLE IF NOT EXISTS settings_changer_targets (
	save_id TEXT,
	entry_name TEXT,
	changer_index INTEGER,
	target_key TEXT,
	scope TEXT,
	zone_index INTEGER,
	zone_id BIGINT,
	has_zone_id BOOLEAN,
	setting_name TEXT,
	value_type TEXT,
	number_value DOUBLE,
	string_value TEXT,
	bool_value BOOLEAN,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS active_species (
	save_id TEXT,
	entry_name TEXT,
	active_species_index INTEGER,
	species_id BIGINT
);

CREATE TABLE IF NOT EXISTS species (
	save_id TEXT,
	entry_name TEXT,
	species_index INTEGER,
	species_id BIGINT,
	has_species_id BOOLEAN,
	parent_id BIGINT,
	has_parent_id BOOLEAN,
	generation_of_first_specimen BIGINT,
	time_creation DOUBLE,
	favorite BOOLEAN,
	generic_name TEXT,
	specific_name TEXT,
	description TEXT,
	template_version TEXT
);

CREATE TABLE IF NOT EXISTS species_genes (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	gene_name TEXT,
	path TEXT,
	value_type TEXT,
	number_value DOUBLE,
	bool_value BOOLEAN,
	string_value TEXT,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS species_brain_nodes (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	node_row_index INTEGER,
	node_index BIGINT,
	innovation BIGINT,
	node_type BIGINT,
	type_name TEXT,
	node_desc TEXT,
	archetype BIGINT,
	base_activation DOUBLE,
	value DOUBLE,
	last_input DOUBLE,
	last_output DOUBLE
);

CREATE TABLE IF NOT EXISTS species_brain_synapses (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	synapse_row_index INTEGER,
	innovation BIGINT,
	node_in BIGINT,
	node_out BIGINT,
	weight DOUBLE,
	enabled BOOLEAN
);

CREATE TABLE IF NOT EXISTS bibites (
	save_id TEXT,
	entry_name TEXT,
	body_id BIGINT,
	has_body_id BOOLEAN,
	species_id BIGINT,
	generation BIGINT,
	dead BOOLEAN,
	dying BOOLEAN,
	health DOUBLE,
	energy DOUBLE,
	time_alive DOUBLE,
	transform_position_x DOUBLE,
	transform_position_y DOUBLE,
	transform_rotation DOUBLE,
	transform_scale DOUBLE,
	rb2d_px DOUBLE,
	rb2d_py DOUBLE,
	rb2d_vx DOUBLE,
	rb2d_vy DOUBLE,
	rb2d_r DOUBLE
);

CREATE TABLE IF NOT EXISTS bibite_genes (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	gene_name TEXT,
	path TEXT,
	value_type TEXT,
	number_value DOUBLE,
	bool_value BOOLEAN,
	string_value TEXT,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS bibite_body (
	save_id TEXT,
	entry_name TEXT,
	body_id BIGINT,
	has_body_id BOOLEAN,
	d2_size DOUBLE,
	fat_reserves_amount DOUBLE,
	attacked_dmg DOUBLE,
	times_attacked DOUBLE,
	total_damage_suffered DOUBLE,
	brain_ticks_count DOUBLE,
	vision_lookup_count DOUBLE,
	vision_sensing_count DOUBLE,
	corpse_energy_offset DOUBLE
);

CREATE TABLE IF NOT EXISTS bibite_mouth (
	save_id TEXT,
	entry_name TEXT,
	body_id BIGINT,
	has_body_id BOOLEAN,
	attacked_last_frame BOOLEAN,
	bibites_bitten DOUBLE,
	bite_progress DOUBLE,
	murdered_area DOUBLE,
	total_damage_dealt DOUBLE,
	total_murders DOUBLE
);

CREATE TABLE IF NOT EXISTS bibite_pheromone_emitters (
	save_id TEXT,
	entry_name TEXT,
	body_id BIGINT,
	has_body_id BOOLEAN,
	progress DOUBLE
);

CREATE TABLE IF NOT EXISTS bibite_egg_layers (
	save_id TEXT,
	entry_name TEXT,
	body_id BIGINT,
	has_body_id BOOLEAN,
	egg_progress DOUBLE,
	n_eggs_laid DOUBLE
);

CREATE TABLE IF NOT EXISTS bibite_control (
	save_id TEXT,
	entry_name TEXT,
	body_id BIGINT,
	has_body_id BOOLEAN,
	total_travel DOUBLE
);

CREATE TABLE IF NOT EXISTS bibite_stomach_contents (
	save_id TEXT,
	entry_name TEXT,
	body_id BIGINT,
	has_body_id BOOLEAN,
	content_index INTEGER,
	material TEXT,
	amount DOUBLE,
	average_chunk_amount DOUBLE
);

CREATE TABLE IF NOT EXISTS bibite_children (
	save_id TEXT,
	entry_name TEXT,
	parent_body_id BIGINT,
	has_parent_id BOOLEAN,
	child_index INTEGER,
	child_body_id BIGINT
);

CREATE TABLE IF NOT EXISTS bibite_brain_nodes (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	node_row_index INTEGER,
	node_index BIGINT,
	innovation BIGINT,
	node_type BIGINT,
	type_name TEXT,
	node_desc TEXT,
	archetype BIGINT,
	base_activation DOUBLE,
	value DOUBLE,
	last_input DOUBLE,
	last_output DOUBLE
);

CREATE TABLE IF NOT EXISTS bibite_brain_synapses (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	synapse_row_index INTEGER,
	innovation BIGINT,
	node_in BIGINT,
	node_out BIGINT,
	weight DOUBLE,
	enabled BOOLEAN
);

CREATE TABLE IF NOT EXISTS eggs (
	save_id TEXT,
	entry_name TEXT,
	egg_id BIGINT,
	has_egg_id BOOLEAN,
	species_id BIGINT,
	generation BIGINT,
	hatch_progress DOUBLE,
	energy DOUBLE,
	transform_position_x DOUBLE,
	transform_position_y DOUBLE,
	transform_rotation DOUBLE,
	transform_scale DOUBLE,
	rb2d_px DOUBLE,
	rb2d_py DOUBLE,
	rb2d_vx DOUBLE,
	rb2d_vy DOUBLE,
	rb2d_r DOUBLE
);

CREATE TABLE IF NOT EXISTS egg_genes (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	gene_name TEXT,
	path TEXT,
	value_type TEXT,
	number_value DOUBLE,
	bool_value BOOLEAN,
	string_value TEXT,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS egg_brain_nodes (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	node_row_index INTEGER,
	node_index BIGINT,
	innovation BIGINT,
	node_type BIGINT,
	type_name TEXT,
	node_desc TEXT,
	archetype BIGINT,
	base_activation DOUBLE,
	value DOUBLE,
	last_input DOUBLE,
	last_output DOUBLE
);

CREATE TABLE IF NOT EXISTS egg_brain_synapses (
	save_id TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	entry_name TEXT,
	synapse_row_index INTEGER,
	innovation BIGINT,
	node_in BIGINT,
	node_out BIGINT,
	weight DOUBLE,
	enabled BOOLEAN
);

CREATE TABLE IF NOT EXISTS pellet_groups (
	save_id TEXT,
	entry_name TEXT,
	group_index INTEGER,
	zone TEXT,
	pellet_count INTEGER
);

CREATE TABLE IF NOT EXISTS pellets (
	save_id TEXT,
	entry_name TEXT,
	pellet_index INTEGER,
	group_index INTEGER,
	zone TEXT,
	material TEXT,
	amount DOUBLE,
	matter_decay_time_alive DOUBLE,
	matter_decay_rot_amount DOUBLE,
	has_matter_decay BOOLEAN,
	transform_position_x DOUBLE,
	transform_position_y DOUBLE,
	transform_rotation DOUBLE,
	transform_scale DOUBLE,
	rb2d_px DOUBLE,
	rb2d_py DOUBLE,
	rb2d_vx DOUBLE,
	rb2d_vy DOUBLE,
	rb2d_r DOUBLE
);

CREATE TABLE IF NOT EXISTS pheromones (
	save_id TEXT,
	entry_name TEXT,
	pheromone_index INTEGER,
	transform_position_x DOUBLE,
	transform_position_y DOUBLE,
	transform_rotation DOUBLE,
	transform_scale DOUBLE,
	heading_raw_json TEXT,
	r_strength DOUBLE,
	g_strength DOUBLE,
	b_strength DOUBLE,
	nr DOUBLE,
	ng DOUBLE,
	nb DOUBLE
);

CREATE TABLE IF NOT EXISTS json_scalars (
	save_id TEXT,
	entry_name TEXT,
	owner_kind TEXT,
	owner_id TEXT,
	path TEXT,
	value_type TEXT,
	number_value DOUBLE,
	string_value TEXT,
	bool_value BOOLEAN,
	raw_json TEXT
);

CREATE OR REPLACE VIEW bibite_mutation_refs AS
SELECT save_id, entry_name, body_id, health, energy, dead, dying, has_body_id
FROM bibites
WHERE has_body_id;
