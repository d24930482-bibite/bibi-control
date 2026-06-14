package duckdb

var allTables = []string{
	"save_archives",
	"save_entries",
	"diagnostics",
	"scenes",
	"vars",
	"scene_color_selectors",
	"scene_phero_towers",
	"scene_rad_towers",
	"settings_simulation_values",
	"settings_independent_values",
	"settings_materials",
	"settings_material_values",
	"settings_zones",
	"settings_zone_geometry",
	"settings_zone_values",
	"settings_zone_groups",
	"settings_bibite_spawners",
	"settings_changers",
	"settings_changer_points",
	"settings_changer_targets",
	"active_species",
	"species",
	"species_genes",
	"species_brain_nodes",
	"species_brain_synapses",
	"bibites",
	"bibite_genes",
	"bibite_body",
	"bibite_mouth",
	"bibite_pheromone_emitters",
	"bibite_egg_layers",
	"bibite_control",
	"bibite_stomach_contents",
	"bibite_children",
	"bibite_brain_nodes",
	"bibite_brain_synapses",
	"eggs",
	"egg_genes",
	"egg_brain_nodes",
	"egg_brain_synapses",
	"pellet_groups",
	"pellets",
	"pheromones",
	"json_scalars",
}

var saveArchiveFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"SourcePath", "source_path"},
	{"FileName", "file_name"},
	{"SizeBytes", "size_bytes"},
	{"SHA256", "sha256"},
}

var saveEntryFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryIndex", "entry_index"},
	{"EntryName", "entry_name"},
	{"Kind", "kind"},
	{"SHA256", "sha256"},
	{"CompressedSize", "compressed_size"},
	{"UncompressedSize", "uncompressed_size"},
	{"HasUTF8BOM", "has_utf8_bom"},
}

var diagnosticFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"Severity", "severity"},
	{"Code", "code"},
	{"Message", "message"},
}

var sceneFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"Version", "version"},
	{"SimulatedTime", "simulated_time"},
	{"HasSimulatedTime", "has_simulated_time"},
	{"ReportedNBibites", "reported_n_bibites"},
	{"HasReportedNBibites", "has_reported_n_bibites"},
	{"ReportedNPellets", "reported_n_pellets"},
	{"HasReportedNPellets", "has_reported_n_pellets"},
	{"ParsedBibites", "parsed_bibites"},
	{"ParsedEggs", "parsed_eggs"},
	{"AliveBibites", "alive_bibites"},
	{"DeadBibites", "dead_bibites"},
	{"DyingBibites", "dying_bibites"},
	{"ParsedPellets", "parsed_pellets"},
}

var varsFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"TowerMaxID", "tower_max_id"},
	{"HasTowerMaxID", "has_tower_max_id"},
}

var sceneColorSelectorFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"ColorSelectorIndex", "color_selector_index"},
	{"RawJSON", "raw_json"},
}

var sceneTowerFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"TowerKind", "tower_kind"},
	{"TowerIndex", "tower_index"},
	{"RawJSON", "raw_json"},
}

var settingValueFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"Scope", "scope"},
	{"OwnerKind", "owner_kind"},
	{"OwnerID", "owner_id"},
	{"SettingName", "setting_name"},
	{"Path", "path"},
	{"Type", "value_type"},
	{"NumberValue", "number_value"},
	{"StringValue", "string_value"},
	{"BoolValue", "bool_value"},
	{"RawJSON", "raw_json"},
	{"WrapperRawJSON", "wrapper_raw_json"},
}

var settingsMaterialFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"MaterialIndex", "material_index"},
	{"MaterialName", "material_name"},
	{"RawJSON", "raw_json"},
}

var settingsZoneFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"ZoneIndex", "zone_index"},
	{"ZoneID", "zone_id"},
	{"HasZoneID", "has_zone_id"},
	{"Name", "name"},
	{"Material", "material"},
	{"Distribution", "distribution"},
	{"RawJSON", "raw_json"},
}

var settingsZoneGeometryFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"ZoneIndex", "zone_index"},
	{"ZoneID", "zone_id"},
	{"HasZoneID", "has_zone_id"},
	{"GeometryIndex", "geometry_index"},
	{"GeometryKind", "geometry_kind"},
	{"PositionX", "position_x"},
	{"PositionY", "position_y"},
	{"Radius", "radius"},
	{"RadiusIsRelative", "radius_is_relative"},
	{"RawJSON", "raw_json"},
}

var settingsZoneGroupFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"GroupIndex", "group_index"},
	{"Name", "name"},
	{"RawJSON", "raw_json"},
}

var settingsBibiteSpawnerFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"SpawnerIndex", "spawner_index"},
	{"Path", "path"},
	{"SpawnPriority", "spawn_priority"},
	{"Minimum", "minimum"},
	{"RandomizeGenes", "randomize_genes"},
	{"GrowthAtSpawn", "growth_at_spawn"},
	{"Tagging", "tagging"},
	{"SpawnType", "spawn_type"},
	{"TotalSpawned", "total_spawned"},
	{"RawJSON", "raw_json"},
}

var settingsChangerFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"ChangerIndex", "changer_index"},
	{"Name", "name"},
	{"Repeat", "repeat_enabled"},
	{"Start", "start_time"},
	{"RawJSON", "raw_json"},
}

var settingsChangerPointFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"ChangerIndex", "changer_index"},
	{"PointIndex", "point_index"},
	{"T", "t"},
	{"Y", "y"},
	{"D", "d"},
	{"F", "f"},
}

var settingsChangerTargetFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"ChangerIndex", "changer_index"},
	{"TargetKey", "target_key"},
	{"Scope", "scope"},
	{"ZoneIndex", "zone_index"},
	{"ZoneID", "zone_id"},
	{"HasZoneID", "has_zone_id"},
	{"SettingName", "setting_name"},
	{"Type", "value_type"},
	{"NumberValue", "number_value"},
	{"StringValue", "string_value"},
	{"BoolValue", "bool_value"},
	{"RawJSON", "raw_json"},
}

var activeSpeciesFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"ActiveSpeciesIndex", "active_species_index"},
	{"SpeciesID", "species_id"},
}

var speciesFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"SpeciesIndex", "species_index"},
	{"SpeciesID", "species_id"},
	{"HasSpeciesID", "has_species_id"},
	{"ParentID", "parent_id"},
	{"HasParentID", "has_parent_id"},
	{"GenerationOfFirstSpecimen", "generation_of_first_specimen"},
	{"TimeCreation", "time_creation"},
	{"Favorite", "favorite"},
	{"GenericName", "generic_name"},
	{"SpecificName", "specific_name"},
	{"Description", "description"},
	{"TemplateVersion", "template_version"},
}

var geneFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"OwnerKind", "owner_kind"},
	{"OwnerID", "owner_id"},
	{"EntryName", "entry_name"},
	{"GeneName", "gene_name"},
	{"Path", "path"},
	{"Type", "value_type"},
	{"NumberValue", "number_value"},
	{"BoolValue", "bool_value"},
	{"StringValue", "string_value"},
	{"RawJSON", "raw_json"},
}

var brainNodeFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"OwnerKind", "owner_kind"},
	{"OwnerID", "owner_id"},
	{"EntryName", "entry_name"},
	{"NodeRowIndex", "node_row_index"},
	{"NodeIndex", "node_index"},
	{"Innovation", "innovation"},
	{"Type", "node_type"},
	{"TypeName", "type_name"},
	{"Desc", "node_desc"},
	{"Archetype", "archetype"},
	{"BaseActivation", "base_activation"},
	{"Value", "value"},
	{"LastInput", "last_input"},
	{"LastOutput", "last_output"},
}

var brainSynapseFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"OwnerKind", "owner_kind"},
	{"OwnerID", "owner_id"},
	{"EntryName", "entry_name"},
	{"SynapseRowIndex", "synapse_row_index"},
	{"Innovation", "innovation"},
	{"NodeIn", "node_in"},
	{"NodeOut", "node_out"},
	{"Weight", "weight"},
	{"Enabled", "enabled"},
}

var bibiteFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"BodyID", "body_id"},
	{"HasBodyID", "has_body_id"},
	{"SpeciesID", "species_id"},
	{"Generation", "generation"},
	{"Dead", "dead"},
	{"Dying", "dying"},
	{"Health", "health"},
	{"Energy", "energy"},
	{"TimeAlive", "time_alive"},
	{"TransformPositionX", "transform_position_x"},
	{"TransformPositionY", "transform_position_y"},
	{"TransformRotation", "transform_rotation"},
	{"TransformScale", "transform_scale"},
	{"RB2DPX", "rb2d_px"},
	{"RB2DPY", "rb2d_py"},
	{"RB2DVX", "rb2d_vx"},
	{"RB2DVY", "rb2d_vy"},
	{"RB2DR", "rb2d_r"},
}

var bibiteBodyFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"BodyID", "body_id"},
	{"HasBodyID", "has_body_id"},
	{"D2Size", "d2_size"},
	{"FatReservesAmount", "fat_reserves_amount"},
	{"AttackedDmg", "attacked_dmg"},
	{"TimesAttacked", "times_attacked"},
	{"TotalDamageSuffered", "total_damage_suffered"},
	{"BrainTicksCount", "brain_ticks_count"},
	{"VisionLookupCount", "vision_lookup_count"},
	{"VisionSensingCount", "vision_sensing_count"},
	{"CorpseEnergyOffset", "corpse_energy_offset"},
}

var bibiteMouthFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"BodyID", "body_id"},
	{"HasBodyID", "has_body_id"},
	{"AttackedLastFrame", "attacked_last_frame"},
	{"BibitesBitten", "bibites_bitten"},
	{"BiteProgress", "bite_progress"},
	{"MurderedArea", "murdered_area"},
	{"TotalDamageDealt", "total_damage_dealt"},
	{"TotalMurders", "total_murders"},
}

var bibitePheromoneEmitterFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"BodyID", "body_id"},
	{"HasBodyID", "has_body_id"},
	{"Progress", "progress"},
}

var bibiteEggLayerFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"BodyID", "body_id"},
	{"HasBodyID", "has_body_id"},
	{"EggProgress", "egg_progress"},
	{"NEggsLaid", "n_eggs_laid"},
}

var bibiteControlFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"BodyID", "body_id"},
	{"HasBodyID", "has_body_id"},
	{"TotalTravel", "total_travel"},
}

var stomachContentFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"BodyID", "body_id"},
	{"HasBodyID", "has_body_id"},
	{"ContentIndex", "content_index"},
	{"Material", "material"},
	{"Amount", "amount"},
	{"AverageChunkAmount", "average_chunk_amount"},
}

var bibiteChildFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"ParentBodyID", "parent_body_id"},
	{"HasParentID", "has_parent_id"},
	{"ChildIndex", "child_index"},
	{"ChildBodyID", "child_body_id"},
}

var eggFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"EggID", "egg_id"},
	{"HasEggID", "has_egg_id"},
	{"SpeciesID", "species_id"},
	{"Generation", "generation"},
	{"HatchProgress", "hatch_progress"},
	{"Energy", "energy"},
	{"TransformPositionX", "transform_position_x"},
	{"TransformPositionY", "transform_position_y"},
	{"TransformRotation", "transform_rotation"},
	{"TransformScale", "transform_scale"},
	{"RB2DPX", "rb2d_px"},
	{"RB2DPY", "rb2d_py"},
	{"RB2DVX", "rb2d_vx"},
	{"RB2DVY", "rb2d_vy"},
	{"RB2DR", "rb2d_r"},
}

var pelletGroupFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"GroupIndex", "group_index"},
	{"Zone", "zone"},
	{"PelletCount", "pellet_count"},
}

var pelletFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"PelletIndex", "pellet_index"},
	{"GroupIndex", "group_index"},
	{"GroupPelletIndex", "group_pellet_index"},
	{"Zone", "zone"},
	{"Material", "material"},
	{"Amount", "amount"},
	{"MatterDecayTimeAlive", "matter_decay_time_alive"},
	{"MatterDecayRotAmount", "matter_decay_rot_amount"},
	{"HasMatterDecay", "has_matter_decay"},
	{"TransformPositionX", "transform_position_x"},
	{"TransformPositionY", "transform_position_y"},
	{"TransformRotation", "transform_rotation"},
	{"TransformScale", "transform_scale"},
	{"RB2DPX", "rb2d_px"},
	{"RB2DPY", "rb2d_py"},
	{"RB2DVX", "rb2d_vx"},
	{"RB2DVY", "rb2d_vy"},
	{"RB2DR", "rb2d_r"},
}

var pheromoneFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"PheromoneIndex", "pheromone_index"},
	{"TransformPositionX", "transform_position_x"},
	{"TransformPositionY", "transform_position_y"},
	{"TransformRotation", "transform_rotation"},
	{"TransformScale", "transform_scale"},
	{"HeadingRawJSON", "heading_raw_json"},
	{"RStrength", "r_strength"},
	{"GStrength", "g_strength"},
	{"BStrength", "b_strength"},
	{"NR", "nr"},
	{"NG", "ng"},
	{"NB", "nb"},
}

var scalarFields = []fieldSpec{
	{"SaveID", "save_id"},
	{"EntryName", "entry_name"},
	{"OwnerKind", "owner_kind"},
	{"OwnerID", "owner_id"},
	{"Path", "path"},
	{"Type", "value_type"},
	{"NumberValue", "number_value"},
	{"StringValue", "string_value"},
	{"BoolValue", "bool_value"},
	{"RawJSON", "raw_json"},
}
