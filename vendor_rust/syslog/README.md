# Sending to Syslog in Rust

[![Build Status](https://github.com/Geal/rust-syslog/actions/workflows/rust.yml/badge.svg)](https://github.com/Geal/rust-syslog/actions/workflows/rust.yml)
![Codecov](https://img.shields.io/codecov/c/github/geal/rust-syslog)
[![crates.io Version](https://img.shields.io/crates/v/syslog.svg)](https://crates.io/crates/syslog)

A small library to send syslog messages to unix sockets, TCP or UDP.

## Installation

syslog is available on [crates.io](https://crates.io/crates/syslog) and can be included in your Cargo enabled project like this:

```toml
[dependencies]
syslog = "^6.0"
```

## documentation

Reference documentation is available [here](https://docs.rs/syslog).

## Example

```rust
extern crate syslog;

use syslog::{Facility, Formatter3164};

fn main() {
  let formatter = Formatter3164 {
    facility: Facility::LOG_USER,
    hostname: None,
    process: "myprogram".into(),
    pid: 42,
  };

  match syslog::unix(formatter) {
    Err(e)         => println!("impossible to connect to syslog: {:?}", e),
    Ok(mut writer) => {
      writer.err("hello world").expect("could not write error message");
    }
  }
}
```

The struct `syslog::Logger` implements `Log` from the `log` crate, so it can be used as backend for other logging systems:

```rust
extern crate syslog;
#[macro_use]
extern crate log;

use syslog::{Facility, Formatter3164, BasicLogger};
use log::{SetLoggerError, LevelFilter};

fn main() {
    let formatter = Formatter3164 {
        facility: Facility::LOG_USER,
        hostname: None,
        process: "myprogram".into(),
        pid: 0,
    };

    let logger = syslog::unix(formatter).expect("could not connect to syslog");
    log::set_boxed_logger(Box::new(BasicLogger::new(logger)))
            .map(|()| log::set_max_level(LevelFilter::Info));

    info!("hello world");
}

```

There are 3 functions to create loggers:

* the `unix` function sends to the local syslog through a Unix socket: `syslog::unix(formatter)`
* the `tcp` function takes an address for a remote TCP syslog server: `tcp(formatter, "127.0.0.1:4242")`
* the `udp` function takes an address for a local port, and the address remote UDP syslog server: `udp(formatter, "127.0.0.1:1234", "127.0.0.1:4242")`
