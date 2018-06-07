use std::process::Command;

fn main() {
    // Generate contracts.
    Command::new("truffle")
        .arg("compile")
        .status()
        .expect("truffle failed to build");

    println!("cargo:rerun-if-changed={}", "contracts/Migrations.sol");
    println!("cargo:rerun-if-changed={}", "contracts/RandomBeacon.sol");
    println!("cargo:rerun-if-changed={}", "contracts/Stake.sol");
}
