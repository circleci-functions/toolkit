/*
Package observability provides observability related functionalities that are
centered around context.Context objects.

See docs/report.md for more information.

For the event-related methods:

• Everything gets a log message at the reported level

• Anything at Info and above gets a trace event

• All error arguments are reported to the error handler at the requested level

• Any report for Warn or higher is reported to the error handler, even if it
does not come with an associated error

• Reports Warnings and context cancellation/timeout at "warn" level instead of "error" level

• Reporting at Panic level panics the application

All logs, traces, and errors are supplemented with base metadata automatically,
such as k8s variables and service name. Users can add their own Fields to
supplement observability data with WithFields.
*/
package observability
