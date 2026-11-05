/* No memory bug. Writes ordinary log text to stderr. */
#include <stdio.h>

int main(void)
{
	fprintf(stderr, "loading config from /etc/example.conf\n");
	fprintf(stderr, "warning: option 'cache' is deprecated\n");
	fprintf(stderr, "error: could not parse line 3: unexpected token\n");
	fprintf(stderr, "steps:\n  #1 read input\n  #2 write output\n");
	fprintf(stderr, "done in 0.01s\n");
	return 0;
}
