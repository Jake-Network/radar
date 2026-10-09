use std::fmt;

pub struct ExportRecord {
    pub dataset_id: String,
    pub rows: u64,
}

impl fmt::Display for ExportRecord {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{},{}", self.dataset_id, self.rows)
    }
}
