#!/usr/bin/env python3
"""Require every combined-path Java method to pass without skips."""

import sys
from pathlib import Path
from xml.etree import ElementTree


EXPECTED = {
    "happyPath",
    "rejectedCredentialsStopAtGateway",
    "givenAcceptedClaimVariants_thenCatalogResultsPreserved",
    "givenTestOwnedSentinel_thenObserverDetectsIdentityAndPreservesTrace",
}


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: check-java-results.py <failsafe-report.xml>")
    try:
        suite = ElementTree.parse(Path(sys.argv[1])).getroot()
        cases = suite.findall("testcase")
        names = [case.attrib["name"] for case in cases]
        if (
            not suite.attrib.get("name", "").endswith(".GatewayProductSecurityIT")
            or len(names) != len(EXPECTED)
            or set(names) != EXPECTED
            or any(names.count(name) != 1 for name in names)
            or any(suite.attrib.get(key) != value for key, value in (
                ("tests", "4"), ("errors", "0"),
                ("failures", "0"), ("skipped", "0"),
            ))
            or any(case.find(tag) is not None for case in cases
                   for tag in ("skipped", "error", "failure"))
        ):
            raise ValueError("missing, duplicate, skipped, or failed method")
    except (OSError, ElementTree.ParseError, KeyError, ValueError) as failure:
        raise SystemExit("SECURITY_JAVA_RESULT_MISMATCH: " + str(failure)) from None
    print("security Java results: four required methods passed without skips")


if __name__ == "__main__":
    main()
