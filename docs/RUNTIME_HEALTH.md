# Runtime health accounting

Every acquired generation lease records a bounded, redacted execution result.
Streaming requests record protocol completion and client cancellation as they
occur. Non-streaming requests now record transport completion, upstream HTTP
status, rejection, and cancellation as well. Source health counters and last
status therefore cover both gateway response modes; administrative probes use
separate release paths and do not pollute ordinary request health.
