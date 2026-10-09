pub fn price() -> u64 { 100 }
#[test]
fn price_is_positive() { assert_eq!(price(), 100); }
