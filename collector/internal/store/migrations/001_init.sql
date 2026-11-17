CREATE EXTENSION IF NOT EXISTS timescaledb;

-- One row per poll and line. Values are in engineering units (already scaled).
CREATE TABLE IF NOT EXISTS machine_readings (
    ts                   timestamptz      NOT NULL,
    unit_id              smallint         NOT NULL,

    machine_state        smallint         NOT NULL, -- 0 STOPPED, 1 HEATING, 2 RUNNING, 3 FAULT
    machine_state_name   text             NOT NULL,
    extruder_rpm         double precision NOT NULL, -- rpm
    motor_current        double precision NOT NULL, -- A
    melt_pressure        double precision NOT NULL, -- bar
    zone1_temp           double precision NOT NULL, -- °C
    zone2_temp           double precision NOT NULL,
    zone3_temp           double precision NOT NULL,
    die_temp             double precision NOT NULL,
    vacuum               double precision NOT NULL, -- mbar below atmosphere
    cooling_water_temp   double precision NOT NULL, -- °C
    haul_off_speed       double precision NOT NULL, -- m/min
    throughput           double precision NOT NULL, -- kg/h
    power_consumption    double precision NOT NULL, -- kW
    pipe_outer_diameter  double precision NOT NULL, -- mm, 0 = no pipe yet
    pipe_wall_thickness  double precision NOT NULL, -- mm, 0 = no pipe yet
    active_fault_code    smallint         NOT NULL, -- 0 = none
    total_produced_length double precision NOT NULL, -- m
    total_produced_weight double precision NOT NULL, -- kg
    motor_load           double precision NOT NULL, -- %
    melt_temp            double precision NOT NULL, -- °C
    melt_pressure_f32    real             NOT NULL, -- bar (FLOAT32 register)
    sim_time_s           bigint           NOT NULL, -- simulator time, s
    alarm_word           integer          NOT NULL, -- bit n = DI 5+n
    active_scenario      smallint         NOT NULL,
    last_fault_code      smallint         NOT NULL,

    running              boolean NOT NULL,
    heating              boolean NOT NULL,
    fault_active         boolean NOT NULL,
    emergency_stop_active boolean NOT NULL,
    target_reached       boolean NOT NULL,
    alarm_motor_overload     boolean NOT NULL,
    alarm_melt_pressure_high boolean NOT NULL,
    alarm_zone_temp_high     boolean NOT NULL,
    alarm_zone_temp_low      boolean NOT NULL,
    alarm_cooling_water_high boolean NOT NULL,
    alarm_vacuum_low         boolean NOT NULL,
    alarm_haul_off_deviation boolean NOT NULL,
    alarm_sensor_failure     boolean NOT NULL,
    alarm_emergency_stop     boolean NOT NULL,

    extruder_rpm_sp      double precision NOT NULL,
    zone1_temp_sp        double precision NOT NULL,
    zone2_temp_sp        double precision NOT NULL,
    zone3_temp_sp        double precision NOT NULL,
    die_temp_sp          double precision NOT NULL,
    haul_off_speed_sp    double precision NOT NULL,
    production_target    double precision NOT NULL, -- m, 0 = no target
    fault_scenario       smallint         NOT NULL,

    PRIMARY KEY (unit_id, ts)
);
-- TimescaleDB hypertable partitioned on ts (1-day chunks); also creates the ts index.
SELECT create_hypertable('machine_readings', by_range('ts', INTERVAL '1 day'), if_not_exists => TRUE);

-- State, fault and connectivity changes detected by the collector.
CREATE TABLE IF NOT EXISTS machine_events (
    id          bigserial   PRIMARY KEY,
    ts          timestamptz NOT NULL,
    unit_id     smallint    NOT NULL,
    event_type  text        NOT NULL, -- state_change | fault | fault_cleared | comm_lost | comm_restored
    from_state  text,
    to_state    text,
    fault_code  smallint,
    message     text
);
CREATE INDEX IF NOT EXISTS machine_events_unit_ts_idx ON machine_events (unit_id, ts DESC);
