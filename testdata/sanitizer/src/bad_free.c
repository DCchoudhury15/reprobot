/* Frees a pointer into the middle of a heap buffer. */
#include <stdio.h>
#include <stdlib.h>

void drop_tail(char *buf)
{
	free(buf + 4);
}

int main(int argc, char **argv)
{
	char *buf = malloc(16);
	buf[0] = (char)argc;
	printf("%d\n", buf[0]);
	drop_tail(argv[0] == NULL ? buf + 1 : buf);
	return 0;
}
