/* Copy of heap_overflow_read.c moved down by eight lines. */

/*
 * Same code and same bug as heap_overflow_read.c. The extra lines here
 * change every line number in the report, but not the bug.
 */


#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int sum_bytes(const char *buf, int n)
{
	int s = 0;
	for (int i = 0; i <= n; i++)
		s += buf[i];
	return s;
}

int checksum(const char *text)
{
	int n = (int)strlen(text);
	char *copy = malloc(n);
	memcpy(copy, text, n);
	int s = sum_bytes(copy, n);
	free(copy);
	return s;
}

int main(int argc, char **argv)
{
	printf("%d\n", checksum(argc > 1 ? argv[1] : "hello world"));
	return 0;
}
