-- Plans de plateau multi-pièces (chantier B) : un plan = pièces, plaques, positions, réglages par pièce.
-- Document JSON ; les impressions créées par execute_plate_slice sont des print_jobs ordinaires.
CREATE TABLE plate_plans (
    id         TEXT PRIMARY KEY,
    doc        TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_plate_plans_created ON plate_plans(created_at);
