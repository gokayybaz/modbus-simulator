-- Columnar compression for chunks older than 7 days, segmented per line.
ALTER TABLE machine_readings SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'unit_id',
    timescaledb.compress_orderby   = 'ts DESC'
);
SELECT add_compression_policy('machine_readings', INTERVAL '7 days', if_not_exists => TRUE);

-- 1-minute rollup for dashboards (continuous aggregate, refreshed every minute).
CREATE MATERIALIZED VIEW IF NOT EXISTS machine_readings_1m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket(INTERVAL '1 minute', ts) AS bucket,
    unit_id,
    last(machine_state, ts)          AS machine_state,
    avg(extruder_rpm)                AS extruder_rpm_avg,
    avg(motor_current)               AS motor_current_avg,
    avg(melt_pressure)               AS melt_pressure_avg,
    max(melt_pressure)               AS melt_pressure_max,
    avg(zone1_temp)                  AS zone1_temp_avg,
    avg(zone2_temp)                  AS zone2_temp_avg,
    avg(zone3_temp)                  AS zone3_temp_avg,
    avg(die_temp)                    AS die_temp_avg,
    avg(melt_temp)                   AS melt_temp_avg,
    avg(vacuum)                      AS vacuum_avg,
    avg(cooling_water_temp)          AS cooling_water_temp_avg,
    avg(haul_off_speed)              AS haul_off_speed_avg,
    avg(throughput)                  AS throughput_avg,
    avg(power_consumption)           AS power_consumption_avg,
    avg(motor_load)                  AS motor_load_avg,
    max(motor_load)                  AS motor_load_max,
    avg(nullif(pipe_outer_diameter, 0)) AS pipe_outer_diameter_avg,
    avg(nullif(pipe_wall_thickness, 0)) AS pipe_wall_thickness_avg,
    last(total_produced_length, ts)  AS total_produced_length,
    last(total_produced_weight, ts)  AS total_produced_weight,
    bit_or(alarm_word)               AS alarm_word_any,
    max(active_fault_code)           AS active_fault_code_max,
    count(*)                         AS samples
FROM machine_readings
GROUP BY bucket, unit_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy('machine_readings_1m',
    start_offset      => INTERVAL '1 hour',
    end_offset        => INTERVAL '1 minute',
    schedule_interval => INTERVAL '1 minute',
    if_not_exists     => TRUE);
